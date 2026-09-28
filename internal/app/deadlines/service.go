// Package deadlines — use cases дедлайнов (спека §3, §5.2, §7.1): CRUD
// с генерацией reminders в одной транзакции, регенерация при смене due_at,
// права (персональный — owner, групповой — admin пишет / member читает).
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
	audit     domain.AuditRepo
	clock     domain.Clock
	log       *slog.Logger
}

func NewService(
	deadlines domain.DeadlineRepo,
	reminders domain.ReminderRepo,
	groups domain.GroupRepo,
	members domain.MembershipRepo,
	audit domain.AuditRepo,
	clock domain.Clock,
	log *slog.Logger,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		deadlines: deadlines, reminders: reminders, groups: groups,
		members: members, audit: audit, clock: clock, log: log,
	}
}

// Create создаёт дедлайн и его reminders одной транзакцией репо (спека §7.1).
// Групповой дедлайн — только admin группы (или superadmin); если reminders не
// переданы, используются default_presets группы. Персональный — только явно
// переданные напоминания. fire_at в прошлом молча не создаётся.
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
		if err := s.requireGroupAdmin(ctx, actor, *in.GroupID); err != nil {
			return nil, err
		}
		d.OwnerUserID = nil
		if len(in.Reminders) == 0 {
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
		map[string]any{"group_id": in.GroupID, "due_at": d.DueAt})
	return &View{Deadline: d, Reminders: planned}, nil
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
		if err != nil {
			return nil, err
		}
		if _, err := s.reminders.Regenerate(ctx, id, planned); err != nil {
			return nil, err
		}
	}

	s.writeAudit(ctx, actor.ID, "deadline.update", "deadline", id,
		map[string]any{"due_changed": dueChanged})
	return s.Get(ctx, actor, id)
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
	if err := s.reminders.CancelByDeadline(ctx, id); err != nil {
		return fmt.Errorf("deadlines: cancel reminders: %w", err)
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
	if err := s.reminders.CancelByDeadline(ctx, id); err != nil {
		return nil, fmt.Errorf("deadlines: cancel reminders: %w", err)
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

// requireWrite — персональный: owner/superadmin; групповой: admin/superadmin.
func (s *Service) requireWrite(ctx context.Context, actor *domain.User, d *domain.Deadline) error {
	if d.GroupID == nil {
		if actor.IsSuperadmin || (d.OwnerUserID != nil && *d.OwnerUserID == actor.ID) {
			return nil
		}
		return fmt.Errorf("%w: deadline id=%d belongs to another user", domain.ErrForbidden, d.ID)
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
