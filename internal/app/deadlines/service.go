// Package deadlines — use cases дедлайнов (спека §3, §5.2, §7.1): CRUD
// с генерацией reminders в одной транзакции, регенерация при смене due_at,
// права (персональный — owner; групповой пишут автор дедлайна и admin, читает
// любой участник) и модерация: дедлайн участника создаётся
// pending_approval и становится active только после подтверждения админом.
package deadlines

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// Границы DTO-валидации (спека §5.2).
const (
	maxTitleRunes       = 200
	maxDescriptionRunes = 2000
	maxReminders        = 10
	minOffset           = 5 * time.Minute
	maxDueHorizon       = 5 * 365 * 24 * time.Hour // ≤ +5 лет
)

// defaultPresets — пресеты по умолчанию (7д/3д/24ч), если у группы пустой
// набор (совпадает с DB-дефолтом default_presets, спека §4).
var defaultPresets = []time.Duration{7 * 24 * time.Hour, 3 * 24 * time.Hour, 24 * time.Hour}

// ReminderSpec — напоминание из запроса: kind + offset_minutes или fire_at.
type ReminderSpec struct {
	Kind          domain.ReminderKind
	OffsetMinutes *int
	FireAt        *time.Time
}

// CreateInput — вход создания дедлайна. GroupID nil → персональный.
type CreateInput struct {
	GroupID     *int64
	Title       string
	Description string
	DueAt       time.Time
	TZ          string
	Reminders   []ReminderSpec
}

// UpdateInput — патч редактирования; nil-поле = «не трогать».
type UpdateInput struct {
	Title       *string
	Description *string
	DueAt       *time.Time
	TZ          *string
}

// ListQuery — фильтры списка (from/to по due_at, status, scope=all).
type ListQuery struct {
	From   *time.Time
	To     *time.Time
	Status *domain.DeadlineStatus
	Scope  string // "" = только личные, "all" = + групповые из membership'ов
}

// View — дедлайн с его reminders (для GET /deadlines/{id}).
type View struct {
	Deadline  *domain.Deadline
	Reminders []domain.Reminder
}

type Service struct {
	deadlines domain.DeadlineRepo
	reminders domain.ReminderRepo
	groups    domain.GroupRepo
	members   domain.MembershipRepo
	bindings  domain.ChatBindingRepo
	users     domain.UserRepo
	audit     domain.AuditRepo
	// notifier — ЛС админам группы о новых pending_approval-дедлайнах и
	// в чат группы при активации. nil — уведомления отключены (тесты).
	notifier domain.Notifier
	clock    domain.Clock
	log      *slog.Logger
}

func NewService(
	deadlines domain.DeadlineRepo,
	reminders domain.ReminderRepo,
	groups domain.GroupRepo,
	members domain.MembershipRepo,
	bindings domain.ChatBindingRepo,
	users domain.UserRepo,
	audit domain.AuditRepo,
	clock domain.Clock,
	log *slog.Logger,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		deadlines: deadlines, reminders: reminders, groups: groups,
		bindings: bindings, users: users, members: members, audit: audit,
		clock: clock, log: log,
	}
}

// WithNotifier подключает доставку уведомлений о модерации дедлайнов.
// Отдельный шаг (как groups.WithOptions): большинству тестовых сборок
// транспорт не нужен.
func (s *Service) WithNotifier(n domain.Notifier) *Service {
	s.notifier = n
	return s
}

// Create создаёт дедлайн и его reminders одной транзакцией репо (спека §7.1).
// Групповой дедлайн от АДМИНА — сразу active с рассылкой уведомлений в чат;
// от обычного участника — pending_approval (группе не виден, уведомления не
// идут) до подтверждения админом (Approve). Если reminders не переданы,
// используются default_presets группы. Персональный — только явно переданные
// напоминания. fire_at в прошлом молча не создаётся.
func (s *Service) Create(ctx context.Context, actor *domain.User, in CreateInput) (*View, error) {
	now := s.clock.Now()

	title := strings.TrimSpace(in.Title)
	if l := utf8.RuneCountInString(title); l < 1 || l > maxTitleRunes {
		return nil, &domain.ValidationError{Field: "title", Msg: "must be 1..200 characters"}
	}
	if utf8.RuneCountInString(in.Description) > maxDescriptionRunes {
		return nil, &domain.ValidationError{Field: "description", Msg: "must be <= 2000 characters"}
	}
	if !in.DueAt.After(now) {
		return nil, &domain.ValidationError{Field: "due_at", Msg: "must be in the future"}
	}
	if in.DueAt.Sub(now) > maxDueHorizon {
		return nil, &domain.ValidationError{Field: "due_at", Msg: "must be within 5 years"}
	}
	if len(in.Reminders) > maxReminders {
		return nil, &domain.ValidationError{Field: "reminders", Msg: "must be <= 10 per deadline"}
	}
	tz := in.TZ
	if tz == "" {
		tz = actor.TZ
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, &domain.ValidationError{Field: "tz", Msg: "unknown timezone"}
	}

	d := &domain.Deadline{
		GroupID:     in.GroupID,
		Title:       title,
		Description: in.Description,
		DueAt:       in.DueAt.UTC(),
		TZ:          tz,
		CreatedBy:   actor.ID,
		Status:      domain.DeadlineStatusActive,
	}

	var presets []time.Duration
	if in.GroupID != nil {
		member, err := s.members.Get(ctx, *in.GroupID, actor.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		isAdmin := actor.IsSuperadmin || (member != nil && member.Role == domain.RoleAdmin)
		if !isAdmin {
			// Участник без роли admin (и не участник вовсе) должен быть
			// участником группы: посторонним создавать групповые дедлайны
			// нельзя. Не-участник → 403 как раньше.
			if member == nil && !actor.IsSuperadmin {
				if _, err := s.groups.GetByID(ctx, *in.GroupID); err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("%w: not a member of group id=%d", domain.ErrForbidden, *in.GroupID)
			}
			if member == nil {
				return nil, fmt.Errorf("%w: not a member of group id=%d", domain.ErrForbidden, *in.GroupID)
			}
			d.Status = domain.DeadlineStatusPendingApproval
		}
		d.OwnerUserID = nil
		if len(in.Reminders) == 0 && isAdmin {
			// Пресеты группы подставляются только активному дедлайну:
			// до апрува напоминания не планируются вовсе.
			g, err := s.groups.GetByID(ctx, *in.GroupID)
			if err != nil {
				return nil, err
			}
			presets = g.DefaultPresets
			if len(presets) == 0 {
				presets = defaultPresets
			}
		}
	} else {
		owner := actor.ID
		d.OwnerUserID = &owner
	}

	planned, err := s.buildReminders(*d, in.Reminders, presets, now)
	if err != nil {
		return nil, err
	}

	if err := s.deadlines.Create(ctx, d, planned); err != nil {
		return nil, err
	}
	s.writeAudit(ctx, actor.ID, "deadline.create", "deadline", d.ID,
		map[string]any{"group_id": in.GroupID, "due_at": d.DueAt, "status": string(d.Status)})

	if d.Status == domain.DeadlineStatusPendingApproval && in.GroupID != nil {
		s.notifyAdminsPending(ctx, *in.GroupID, d)
	}
	return &View{Deadline: d, Reminders: planned}, nil
}

// Approve — подтверждение админом группового дедлайна в статусе
// pending_approval: переводит его в active, планирует напоминания (пресеты
// группы, если автор не задал свои) и рассылает групповые уведомления.
// Идемпотентность опущена намеренно: повторный approve неактивного
// дедлайна → ErrConflict (состояние уже изменено, тихий успех врал бы).
func (s *Service) Approve(ctx context.Context, actor *domain.User, id int64) (*View, error) {
	d, err := s.deadlines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.GroupID == nil {
		return nil, fmt.Errorf("%w: deadline id=%d is not a group deadline", domain.ErrValidation, id)
	}
	if err := s.requireGroupAdmin(ctx, actor, *d.GroupID); err != nil {
		return nil, err
	}
	if d.Status != domain.DeadlineStatusPendingApproval {
		return nil, fmt.Errorf("%w: deadline id=%d is not pending approval", domain.ErrConflict, id)
	}

	if err := s.deadlines.SetStatus(ctx, id, domain.DeadlineStatusActive); err != nil {
		return nil, err
	}
	d.Status = domain.DeadlineStatusActive

	// Напоминания: если автор задал свои — регенерируем их от due_at;
	// иначе подставляем пресеты группы (как при создании админом).
	planned, err := s.replan(ctx, d, s.clock.Now())
	if err != nil {
		return nil, err
	}
	if len(planned) == 0 {
		g, err := s.groups.GetByID(ctx, *d.GroupID)
		if err != nil {
			return nil, err
		}
		presets := g.DefaultPresets
		if len(presets) == 0 {
			presets = defaultPresets
		}
		planned = domain.PlanReminders(*d, presets, s.clock.Now())
	}
	if len(planned) > 0 {
		if _, err := s.reminders.Regenerate(ctx, id, planned); err != nil {
			return nil, err
		}
	}

	s.writeAudit(ctx, actor.ID, "deadline.approve", "deadline", id,
		map[string]any{"group_id": d.GroupID})
	s.announceGroup(ctx, *d.GroupID, d, "deadline.approved")
	return s.Get(ctx, actor, id)
}

// Reject — отклонение админом группового дедлайна в pending_approval:
// статус rejected, напоминания (планировались бы при апруве) не создаются.
func (s *Service) Reject(ctx context.Context, actor *domain.User, id int64) (*View, error) {
	d, err := s.deadlines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.GroupID == nil {
		return nil, fmt.Errorf("%w: deadline id=%d is not a group deadline", domain.ErrValidation, id)
	}
	if err := s.requireGroupAdmin(ctx, actor, *d.GroupID); err != nil {
		return nil, err
	}
	if d.Status != domain.DeadlineStatusPendingApproval {
		return nil, fmt.Errorf("%w: deadline id=%d is not pending approval", domain.ErrConflict, id)
	}
	if err := s.deadlines.SetStatus(ctx, id, domain.DeadlineStatusRejected); err != nil {
		return nil, err
	}
	d.Status = domain.DeadlineStatusRejected
	s.writeAudit(ctx, actor.ID, "deadline.reject", "deadline", id,
		map[string]any{"group_id": d.GroupID})
	return &View{Deadline: d}, nil
}

// ListPendingGroup — дедлайны группы в pending_approval (админский список
// модерации).
func (s *Service) ListPendingGroup(ctx context.Context, actor *domain.User, groupID int64) ([]domain.Deadline, error) {
	if err := s.requireGroupAdmin(ctx, actor, groupID); err != nil {
		return nil, err
	}
	status := domain.DeadlineStatusPendingApproval
	return s.deadlines.ListByGroup(ctx, groupID, nil, nil, &status)
}

// notifyAdminsPending — best-effort ЛС админам группы о новом дедлайне,
// ждущем апрува (кроме автора: он и так знает).
func (s *Service) notifyAdminsPending(ctx context.Context, groupID int64, d *domain.Deadline) {
	if s.notifier == nil {
		return
	}
	mems, err := s.members.ListByGroup(ctx, groupID)
	if err != nil {
		s.log.Warn("deadlines: list members for notify failed", slog.String("error", err.Error()))
		return
	}
	for _, m := range mems {
		if m.Role != domain.RoleAdmin || m.UserID == d.CreatedBy {
			continue
		}
		u, err := s.users.GetByID(ctx, m.UserID)
		if err != nil {
			continue
		}
		if u.BotBlocked || u.IsBanned {
			continue
		}
		g, err := s.groups.GetByID(ctx, groupID)
		if err != nil {
			return
		}
		text := i18n.T("deadline.pending_notify",
			i18n.EscapeHTML(g.Title), i18n.EscapeHTML(d.Title))
		if err := s.notifier.SendToUser(ctx, u.TelegramID, text); err != nil {
			s.log.Warn("deadlines: admin notify failed",
				slog.Int64("user_id", m.UserID), slog.String("error", err.Error()))
		}
	}
}

// announceGroup — сообщение в привязанный чат группы об активации дедлайна
// (approve): рассылка групповых уведомлений начинается только с этого момента.
func (s *Service) announceGroup(ctx context.Context, groupID int64, d *domain.Deadline, event string) {
	if s.notifier == nil {
		return
	}
	b, err := s.bindings.GetByGroup(ctx, groupID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			s.log.Warn("deadlines: binding load failed", slog.String("error", err.Error()))
		}
		return
	}
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return
	}
	var threadID int64
	if b.MessageThreadID != nil {
		threadID = *b.MessageThreadID
	}
	text := i18n.T("deadline.approved_announce",
		i18n.EscapeHTML(d.Title), i18n.EscapeHTML(g.Slug))
	if err := s.notifier.SendToChat(ctx, b.ChatID, threadID, text); err != nil {
		s.log.Warn("deadlines: group announce failed",
			slog.String("event", event),
			slog.Int64("group_id", groupID),
			slog.String("error", err.Error()))
	}
}

// buildReminders собирает доменные reminders из явных спеков и пресетов:
// kind=preset → fire_at = due_at - offset (через PlanReminders: прошлое и
// дубликаты отсеиваются); custom_offset → fire_at = now + offset; custom_at →
// точное время (только будущее).
func (s *Service) buildReminders(d domain.Deadline, specs []ReminderSpec, presets []time.Duration, now time.Time) ([]domain.Reminder, error) {
	var presetOffsets []time.Duration
	out := make([]domain.Reminder, 0, len(specs)+len(presets))

	for _, spec := range specs {
		switch spec.Kind {
		case domain.KindPreset:
			if spec.OffsetMinutes == nil {
				return nil, &domain.ValidationError{Field: "reminders", Msg: "preset requires offset_minutes"}
			}
			off := time.Duration(*spec.OffsetMinutes) * time.Minute
			if off < minOffset {
				return nil, &domain.ValidationError{Field: "reminders", Msg: "offset must be >= 5 minutes"}
			}
			presetOffsets = append(presetOffsets, off)
		case domain.KindCustomOffset:
			if spec.OffsetMinutes == nil {
				return nil, &domain.ValidationError{Field: "reminders", Msg: "custom_offset requires offset_minutes"}
			}
			off := time.Duration(*spec.OffsetMinutes) * time.Minute
			if off < minOffset {
				return nil, &domain.ValidationError{Field: "reminders", Msg: "offset must be >= 5 minutes"}
			}
			if r, ok := domain.NewCustomOffsetReminder(d.ID, off, now); ok {
				out = append(out, r)
			}
		case domain.KindCustomAt:
			if spec.FireAt == nil {
				return nil, &domain.ValidationError{Field: "reminders", Msg: "custom_at requires fire_at"}
			}
			if !spec.FireAt.After(now) {
				return nil, &domain.ValidationError{Field: "reminders", Msg: "fire_at must be in the future"}
			}
			out = append(out, domain.NewCustomAtReminder(d.ID, spec.FireAt.UTC()))
		default:
			return nil, &domain.ValidationError{Field: "reminders", Msg: "kind must be preset, custom_offset or custom_at"}
		}
	}

	presetOffsets = append(presetOffsets, presets...)
	out = append(out, domain.PlanReminders(d, presetOffsets, now)...)
	return out, nil
}

// Get возвращает дедлайн с reminders. Персональный — owner/superadmin;
// групповой — участник группы/superadmin.
func (s *Service) Get(ctx context.Context, actor *domain.User, id int64) (*View, error) {
	d, err := s.deadlines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireRead(ctx, actor, d); err != nil {
		return nil, err
	}
	reminders, err := s.reminders.ListByDeadline(ctx, id)
	if err != nil {
		return nil, err
	}
	return &View{Deadline: d, Reminders: reminders}, nil
}

// Update меняет поля дедлайна; смена due_at перегенерирует reminders
// (спека §7.1): набор offsets текущих pending preset/custom_offset сохраняется
// и пересчитывается от нового due_at; custom_at воссоздаются как есть, если их
// fire_at ещё в будущем, иначе отбрасываются.
func (s *Service) Update(ctx context.Context, actor *domain.User, id int64, in UpdateInput) (*View, error) {
	d, err := s.deadlines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, actor, d); err != nil {
		return nil, err
	}
	now := s.clock.Now()

	patch := domain.DeadlinePatch{}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if l := utf8.RuneCountInString(t); l < 1 || l > maxTitleRunes {
			return nil, &domain.ValidationError{Field: "title", Msg: "must be 1..200 characters"}
		}
		patch.Title = &t
	}
	if in.Description != nil {
		if utf8.RuneCountInString(*in.Description) > maxDescriptionRunes {
			return nil, &domain.ValidationError{Field: "description", Msg: "must be <= 2000 characters"}
		}
		descr := *in.Description
		patch.Description = &descr
	}
	if in.TZ != nil {
		if _, err := time.LoadLocation(*in.TZ); err != nil {
			return nil, &domain.ValidationError{Field: "tz", Msg: "unknown timezone"}
		}
		tz := *in.TZ
		patch.TZ = &tz
	}
	dueChanged := false
	if in.DueAt != nil {
		due := in.DueAt.UTC()
		if !due.After(now) {
			return nil, &domain.ValidationError{Field: "due_at", Msg: "must be in the future"}
		}
		if due.Sub(now) > maxDueHorizon {
			return nil, &domain.ValidationError{Field: "due_at", Msg: "must be within 5 years"}
		}
		dueChanged = !due.Equal(d.DueAt)
		patch.DueAt = &due
	}

	if err := s.deadlines.Update(ctx, id, patch); err != nil {
		return nil, err
	}

	// Компенсация (unit-of-work отложен): Update и Regenerate — две отдельные
	// транзакции, поэтому сбой регенерации откатывает поля дедлайна обратно.
	// Контракт описан в doc domain.ReminderRepo.Regenerate.
	old := *d
	if patch.DueAt != nil {
		d.DueAt = *patch.DueAt
	}
	if patch.Title != nil {
		d.Title = *patch.Title
	}
	if patch.Description != nil {
		d.Description = *patch.Description
	}
	if patch.TZ != nil {
		d.TZ = *patch.TZ
	}

	if dueChanged {
		planned, err := s.replan(ctx, d, now)
		if err == nil {
			_, err = s.reminders.Regenerate(ctx, id, planned)
		}
		if err != nil {
			s.compensateUpdate(ctx, id, &old, patch)
			return nil, err
		}
	}

	s.writeAudit(ctx, actor.ID, "deadline.update", "deadline", id,
		map[string]any{"due_changed": dueChanged})
	return s.Get(ctx, actor, id)
}

// compensateUpdate возвращает поля дедлайна к old после сбоя Regenerate:
// due_at и изменённые поля откатываются обратным патчем. Провал самой
// компенсации не скрывает исходную ошибку — только log.Error с id дедлайна
// (аудит-хук: рассинхрон дедлайна и reminders чинится следующей правкой
// due_at либо Task 9 воркер защитно пропустит reminders done/deleted).
func (s *Service) compensateUpdate(ctx context.Context, id int64, old *domain.Deadline, applied domain.DeadlinePatch) {
	revert := domain.DeadlinePatch{}
	if applied.Title != nil {
		t := old.Title
		revert.Title = &t
	}
	if applied.Description != nil {
		descr := old.Description
		revert.Description = &descr
	}
	if applied.DueAt != nil {
		due := old.DueAt
		revert.DueAt = &due
	}
	if applied.TZ != nil {
		tz := old.TZ
		revert.TZ = &tz
	}
	if err := s.deadlines.Update(ctx, id, revert); err != nil {
		s.log.Error("deadline update compensation failed: deadline and reminders may be out of sync",
			slog.Int64("deadline_id", id),
			slog.String("error", err.Error()),
		)
	}
}

// replan собирает новый набор reminders после смены due_at из текущих pending:
// offsets preset пересчитываются от нового due_at (PlanReminders отсеивает
// прошлое и дубликаты), custom_offset пересчитывается так же, но сохраняет
// свой kind, custom_at воссоздаются как есть только если ещё в будущем.
func (s *Service) replan(ctx context.Context, d *domain.Deadline, now time.Time) ([]domain.Reminder, error) {
	existing, err := s.reminders.ListByDeadline(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	var presetOffsets, customOffsets []time.Duration
	out := make([]domain.Reminder, 0, len(existing))
	for _, r := range existing {
		if r.Status != domain.ReminderStatusPending {
			continue
		}
		switch r.Kind {
		case domain.KindPreset:
			if r.OffsetMinutes != nil {
				presetOffsets = append(presetOffsets, time.Duration(*r.OffsetMinutes)*time.Minute)
			}
		case domain.KindCustomOffset:
			if r.OffsetMinutes != nil {
				customOffsets = append(customOffsets, time.Duration(*r.OffsetMinutes)*time.Minute)
			}
		case domain.KindCustomAt:
			if r.FireAt.After(now) {
				out = append(out, domain.NewCustomAtReminder(d.ID, r.FireAt))
			}
		}
	}
	// custom_offset — как PlanReminders, но со своим kind: fire_at = due - off,
	// прошлое и дубликаты отсеиваются.
	seen := make(map[time.Duration]bool, len(customOffsets))
	for _, off := range customOffsets {
		if seen[off] {
			continue
		}
		seen[off] = true
		fireAt := d.DueAt.Add(-off)
		if !fireAt.After(now) {
			continue
		}
		mins := int(off / time.Minute)
		out = append(out, domain.Reminder{
			DeadlineID:    d.ID,
			Kind:          domain.KindCustomOffset,
			OffsetMinutes: &mins,
			FireAt:        fireAt,
			Status:        domain.ReminderStatusPending,
		})
	}
	out = append(out, domain.PlanReminders(*d, presetOffsets, now)...)
	return out, nil
}

// Delete — soft delete + отмена всех pending reminders.
func (s *Service) Delete(ctx context.Context, actor *domain.User, id int64) error {
	d, err := s.deadlines.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.requireWrite(ctx, actor, d); err != nil {
		return err
	}
	if err := s.deadlines.SoftDelete(ctx, id); err != nil {
		return err
	}
	// Сбой отмены reminders не отклоняет запрос: дедлайн уже удалён, а воркер
	// (Task 9) защитно пропускает reminders soft-deleted дедлайнов.
	if err := s.reminders.CancelByDeadline(ctx, id); err != nil {
		s.log.Error("deadline deleted but reminder cancellation failed",
			slog.Int64("deadline_id", id),
			slog.String("error", err.Error()),
		)
	}
	s.writeAudit(ctx, actor.ID, "deadline.delete", "deadline", id, nil)
	return nil
}

// Complete — статус done + отмена pending reminders (выполненный дедлайн
// не должен напоминать о себе).
func (s *Service) Complete(ctx context.Context, actor *domain.User, id int64) (*View, error) {
	d, err := s.deadlines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireWrite(ctx, actor, d); err != nil {
		return nil, err
	}
	if err := s.deadlines.SetStatus(ctx, id, domain.DeadlineStatusDone); err != nil {
		return nil, err
	}
	// Как в Delete: статус уже changed, сбой отмены — только log.Error
	// (воркер Task 9 защитно пропустит reminders done-дедлайна).
	if err := s.reminders.CancelByDeadline(ctx, id); err != nil {
		s.log.Error("deadline completed but reminder cancellation failed",
			slog.Int64("deadline_id", id),
			slog.String("error", err.Error()),
		)
	}
	s.writeAudit(ctx, actor.ID, "deadline.complete", "deadline", id, nil)
	return s.Get(ctx, actor, id)
}

// ListGroup — дедлайны группы (читает любой участник или superadmin).
func (s *Service) ListGroup(ctx context.Context, actor *domain.User, groupID int64, q ListQuery) ([]domain.Deadline, error) {
	if !actor.IsSuperadmin {
		if _, err := s.members.Get(ctx, groupID, actor.ID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("%w: not a member of group id=%d", domain.ErrForbidden, groupID)
			}
			return nil, err
		}
	}
	return s.deadlines.ListByGroup(ctx, groupID, q.From, q.To, q.Status)
}

// ListMine — персональные дедлайны; scope=all добавляет групповые из всех
// membership'ов пользователя (агрегация, сортировка по due_at ASC).
func (s *Service) ListMine(ctx context.Context, actor *domain.User, q ListQuery) ([]domain.Deadline, error) {
	out, err := s.deadlines.ListByOwner(ctx, actor.ID, q.From, q.To, q.Status)
	if err != nil {
		return nil, err
	}
	if q.Scope == "all" {
		mems, err := s.members.ListByUser(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range mems {
			groupList, err := s.deadlines.ListByGroup(ctx, m.GroupID, q.From, q.To, q.Status)
			if err != nil {
				return nil, err
			}
			out = append(out, groupList...)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].DueAt.Before(out[j].DueAt) })
	}
	return out, nil
}

// requireRead — персональный: owner/superadmin; групповой: участник/superadmin.
func (s *Service) requireRead(ctx context.Context, actor *domain.User, d *domain.Deadline) error {
	if d.GroupID == nil {
		if actor.IsSuperadmin || (d.OwnerUserID != nil && *d.OwnerUserID == actor.ID) {
			return nil
		}
		return fmt.Errorf("%w: deadline id=%d belongs to another user", domain.ErrForbidden, d.ID)
	}
	if actor.IsSuperadmin {
		return nil
	}
	if _, err := s.members.Get(ctx, *d.GroupID, actor.ID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: not a member of group id=%d", domain.ErrForbidden, *d.GroupID)
		}
		return err
	}
	return nil
}

// requireWrite — право записи по дедлайну (спека §5.2 «автор/admin»):
// персональный — owner/superadmin; групповой — автора дедлайна (created_by),
// админ группы или superadmin. Автор пишет свой дедлайн, даже будучи простым
// участником: он его создал и отвечает за срок. Права на ЧУЖИЕ групповые
// дедлайны остаются у админа группы.
func (s *Service) requireWrite(ctx context.Context, actor *domain.User, d *domain.Deadline) error {
	if d.GroupID == nil {
		if actor.IsSuperadmin || (d.OwnerUserID != nil && *d.OwnerUserID == actor.ID) {
			return nil
		}
		return fmt.Errorf("%w: deadline id=%d belongs to another user", domain.ErrForbidden, d.ID)
	}
	if actor.IsSuperadmin || d.CreatedBy == actor.ID {
		// Существование группы проверяется и на этом пути: удалённая группа
		// должна давать 404, а не «нельзя».
		if _, err := s.groups.GetByID(ctx, *d.GroupID); err != nil {
			return err
		}
		return nil
	}
	return s.requireGroupAdmin(ctx, actor, *d.GroupID)
}

// requireGroupAdmin — роль admin в группе или superadmin; иначе ErrForbidden.
// Существование группы проверяется (404 для несуществующей).
func (s *Service) requireGroupAdmin(ctx context.Context, actor *domain.User, groupID int64) error {
	if _, err := s.groups.GetByID(ctx, groupID); err != nil {
		return err
	}
	if actor.IsSuperadmin {
		return nil
	}
	m, err := s.members.Get(ctx, groupID, actor.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: group id=%d requires admin role", domain.ErrForbidden, groupID)
		}
		return err
	}
	if m.Role != domain.RoleAdmin {
		return fmt.Errorf("%w: group id=%d requires admin role", domain.ErrForbidden, groupID)
	}
	return nil
}

// writeAudit — best-effort запись в audit_log (сбой аудита не роняет операцию).
func (s *Service) writeAudit(ctx context.Context, actorID int64, action, targetType string, targetID int64, meta map[string]any) {
	actor := actorID
	e := &domain.AuditEntry{
		ActorUserID: &actor,
		Action:      action,
		TargetType:  targetType,
		TargetID:    &targetID,
		Meta:        meta,
	}
	if err := s.audit.Write(ctx, e); err != nil {
		s.log.Warn("audit write failed",
			slog.String("action", action),
			slog.String("error", err.Error()),
		)
	}
}
