// Package groups — use cases групп (спека §3, §5.2, §6.4): создание с
// нормализацией и валидацией слага, антиспам-лимиты (3/сутки, 5/неделю),
// поиск, membership-операции и инвайт-коды. Claim — отдельная задача (Task 10).
package groups

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// ErrLastAdmin — маркер конфликтов «последний админ группы» (обёрнут в
// domain.ErrConflict): понижение, кик или выход единственного админа.
var ErrLastAdmin = errors.New("last admin: transfer admin role first")

// Config — лимиты и TTL из env (спека §8).
type Config struct {
	PendingTTL       time.Duration // TTL pending-группы (дефолт 14 дней)
	CreateDayLimit   int           // лимит создания групп в сутки (3)
	CreateWeekLimit  int           // лимит создания групп в неделю (5)
	InviteDefaultTTL time.Duration // дефолтный TTL инвайта (7 дней)
}

// SearchLimit — максимальное число групп в ответе поиска (подсказка слага).
const SearchLimit = 20

// inviteAlphabet — алфавит инвайт-кодов без неоднозначных символов (0/O, 1/I/L).
const inviteAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const inviteCodeLen = 8

type Service struct {
	groups   domain.GroupRepo
	members  domain.MembershipRepo
	invites  domain.InviteRepo
	counters domain.CounterRepo
	audit    domain.AuditRepo
	bindings domain.ChatBindingRepo
	cfg      Config
	clock    domain.Clock
	log      *slog.Logger
}

func NewService(
	groups domain.GroupRepo,
	members domain.MembershipRepo,
	invites domain.InviteRepo,
	counters domain.CounterRepo,
	audit domain.AuditRepo,
	bindings domain.ChatBindingRepo,
	cfg Config,
	clock domain.Clock,
	log *slog.Logger,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		groups: groups, members: members, invites: invites,
		counters: counters, audit: audit, bindings: bindings,
		cfg: cfg, clock: clock, log: log,
	}
}

// HashInviteCode — SHA-256 hex кода: в БД хранится только хэш (колонка
// invites.code), plaintext возвращается клиенту один раз при создании.
func HashInviteCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// generateInviteCode — 8 символов из inviteAlphabet на crypto/rand.
func generateInviteCode() (string, error) {
	b := make([]byte, inviteCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("groups: generate invite code: %w", err)
	}
	code := make([]byte, inviteCodeLen)
	for i, v := range b {
		code[i] = inviteAlphabet[int(v)%len(inviteAlphabet)]
	}
	return string(code), nil
}

// GroupView — агрегированный ответ GET /groups/{id}: группа, роль вызывающего,
// привязка чата и число участников.
type GroupView struct {
	Group        *domain.Group
	Role         domain.Role // "" если вызывающий не участник
	Binding      *domain.ChatBinding
	MembersCount int
}

// MyGroup — группа из ListMine вместе с ролью пользователя.
type MyGroup struct {
	Group domain.Group
	Role  domain.Role
}

// Create создаёт pending-группу (спека §3.1: создатель НЕ админ — роль
// выдаётся через claim) с антиспам-лимитами §3.3. Порядок: лимиты → группа →
// membership создателя (member) → аудит.
func (s *Service) Create(ctx context.Context, actor *domain.User, slugRaw, title string) (*domain.Group, error) {
	slug := domain.Normalize(slugRaw)
	if !actor.IsSuperadmin {
		if err := domain.ValidateStrict(slug); err != nil {
			return nil, err
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, &domain.ValidationError{Field: "title", Msg: "must not be empty"}
	}

	now := s.clock.Now()
	// Дневной лимит проверяется первым — отклонение дешевле (окно короче).
	if err := s.checkCreateLimit(ctx, actor.ID, "group_create_day", 24*time.Hour, s.cfg.CreateDayLimit, now); err != nil {
		return nil, err
	}
	if err := s.checkCreateLimit(ctx, actor.ID, "group_create_week", 168*time.Hour, s.cfg.CreateWeekLimit, now); err != nil {
		return nil, err
	}

	claimExpires := now.Add(s.cfg.PendingTTL)
	g := &domain.Group{
		Slug:           slug,
		Title:          title,
		Status:         domain.GroupStatusPending,
		CreatedBy:      actor.ID,
		ClaimExpiresAt: &claimExpires,
	}
	if err := s.groups.Create(ctx, g); err != nil {
		return nil, err // ErrConflict (слаг занят) проходит насквозь
	}

	m := &domain.Membership{GroupID: g.ID, UserID: actor.ID, Role: domain.RoleMember}
	if err := s.members.Upsert(ctx, m); err != nil {
		return nil, fmt.Errorf("groups: creator membership: %w", err)
	}

	s.writeAudit(ctx, actor.ID, "group.create", "group", g.ID, map[string]any{"slug": g.Slug})
	return g, nil
}

// checkCreateLimit инкрементирует счётчик окна и отклоняет превышение.
// Начало окна floor-ится на слое приложения (репо принимает его как есть).
func (s *Service) checkCreateLimit(ctx context.Context, userID int64, action string, window time.Duration, limit int, now time.Time) error {
	windowStart := now.Truncate(window)
	count, err := s.counters.IncAndCheck(ctx, userID, action, windowStart, limit)
	if err != nil {
		return fmt.Errorf("groups: counter %s: %w", action, err)
	}
	if count > limit {
		return &domain.RateLimitError{RetryAfter: window - now.Sub(windowStart)}
	}
	return nil
}

// Search — подсказка слага по префиксу: активные группы + свои pending
// (фильтр на стороне репо, спека §6.4), не более SearchLimit результатов.
// Роль вызывающего проставляется для групп, где он участник (иначе "").
func (s *Service) Search(ctx context.Context, actor *domain.User, q string) ([]MyGroup, error) {
	q = domain.Normalize(q)
	found, err := s.groups.SearchByPrefix(ctx, q, actor.ID, SearchLimit)
	if err != nil {
		return nil, err
	}
	mems, err := s.members.ListByUser(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	roles := make(map[int64]domain.Role, len(mems))
	for _, m := range mems {
		roles[m.GroupID] = m.Role
	}
	out := make([]MyGroup, 0, len(found))
	for _, g := range found {
		out = append(out, MyGroup{Group: g, Role: roles[g.ID]})
	}
	return out, nil
}

// Get возвращает группу с ролью вызывающего, привязкой чата и счётчиком
// участников. Pending-группа видна только создателю (и superadmin).
func (s *Service) Get(ctx context.Context, actor *domain.User, groupID int64) (*GroupView, error) {
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if err := s.checkVisible(g, actor); err != nil {
		return nil, err
	}

	view := &GroupView{Group: g}
	m, err := s.members.Get(ctx, groupID, actor.ID)
	switch {
	case err == nil:
		view.Role = m.Role
	case errors.Is(err, domain.ErrNotFound):
		// Не участник — роль пустая, но активная группа читаема.
	default:
		return nil, err
	}

	mems, err := s.members.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	view.MembersCount = len(mems)

	b, err := s.bindings.GetByGroup(ctx, groupID)
	switch {
	case err == nil:
		view.Binding = b
	case errors.Is(err, domain.ErrNotFound):
	default:
		return nil, err
	}
	return view, nil
}

// checkVisible — pending-группы видны только создателю и superadmin.
func (s *Service) checkVisible(g *domain.Group, actor *domain.User) error {
	if g.Status == domain.GroupStatusPending && g.CreatedBy != actor.ID && !actor.IsSuperadmin {
		return fmt.Errorf("%w: pending group id=%d", domain.ErrNotFound, g.ID)
	}
	return nil
}

// Update меняет title и/или default_presets; требует роль admin (или
// superadmin). Пресеты: ≤10 штук, каждый ≥5 минут.
func (s *Service) Update(ctx context.Context, actor *domain.User, groupID int64, title *string, defaultPresets *[]time.Duration) (*domain.Group, error) {
	if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return nil, err
	}
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	if title != nil {
		t := strings.TrimSpace(*title)
		if t == "" {
			return nil, &domain.ValidationError{Field: "title", Msg: "must not be empty"}
		}
		g.Title = t
	}
	if defaultPresets != nil {
		presets := *defaultPresets
		if len(presets) == 0 || len(presets) > 10 {
			return nil, &domain.ValidationError{Field: "default_presets", Msg: "must contain 1..10 items"}
		}
		for _, p := range presets {
			if p < 5*time.Minute {
				return nil, &domain.ValidationError{Field: "default_presets", Msg: "each preset must be >= 5 minutes"}
			}
		}
		g.DefaultPresets = presets
	}

	if err := s.groups.Update(ctx, g); err != nil {
		return nil, err
	}
	s.writeAudit(ctx, actor.ID, "group.update", "group", g.ID, nil)
	return g, nil
}

// Delete — soft delete группы админом или superadmin.
func (s *Service) Delete(ctx context.Context, actor *domain.User, groupID int64) error {
	if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return err
	}
	if err := s.groups.SoftDelete(ctx, groupID); err != nil {
		return err
	}
	s.writeAudit(ctx, actor.ID, "group.delete", "group", groupID, nil)
	return nil
}

// ListMine — группы участника с его ролями.
func (s *Service) ListMine(ctx context.Context, actor *domain.User) ([]MyGroup, error) {
	groupList, err := s.groups.ListMine(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	mems, err := s.members.ListByUser(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	roles := make(map[int64]domain.Role, len(mems))
	for _, m := range mems {
		roles[m.GroupID] = m.Role
	}
	out := make([]MyGroup, 0, len(groupList))
	for _, g := range groupList {
		out = append(out, MyGroup{Group: g, Role: roles[g.ID]})
	}
	return out, nil
}

// ListMembers — полный список участников с именами (один JOIN). Доступен
// участникам группы и superadmin.
func (s *Service) ListMembers(ctx context.Context, actor *domain.User, groupID int64) ([]domain.MembershipDetail, error) {
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if err := s.checkVisible(g, actor); err != nil {
		return nil, err
	}
	if !actor.IsSuperadmin {
		if _, err := s.members.Get(ctx, groupID, actor.ID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("%w: not a member of group id=%d", domain.ErrForbidden, groupID)
			}
			return nil, err
		}
	}
	return s.members.ListByGroupDetailed(ctx, groupID)
}

// maxInviteTTL — верхняя граница TTL инвайта (90 дней).
const maxInviteTTL = 90 * 24 * time.Hour

// CreateInvite генерирует инвайт-код (admin). maxUses: -1 = без лимита,
// ≥1 — число использований; 0 и < -1 — ErrValidation. ttl ≤ 0 → дефолт из
// конфига; ttl > 90 дней — ErrValidation. Возвращает plaintext-код
// (показывается один раз) и сохранённый инвайт с хэшем.
func (s *Service) CreateInvite(ctx context.Context, actor *domain.User, groupID int64, role domain.Role, maxUses int, ttl time.Duration) (string, *domain.Invite, error) {
	if role != domain.RoleAdmin && role != domain.RoleMember {
		return "", nil, &domain.ValidationError{Field: "role", Msg: "must be admin or member"}
	}
	if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return "", nil, err
	}
	if maxUses == 0 || maxUses < -1 {
		return "", nil, &domain.ValidationError{Field: "max_uses", Msg: "must be -1 (unlimited) or >= 1"}
	}
	if ttl < 0 || ttl > maxInviteTTL {
		return "", nil, &domain.ValidationError{Field: "ttl_hours", Msg: "must be within 0..2160 hours"}
	}
	if ttl == 0 {
		ttl = s.cfg.InviteDefaultTTL
	}

	code, err := generateInviteCode()
	if err != nil {
		return "", nil, err
	}
	inv := &domain.Invite{
		GroupID:   groupID,
		Code:      HashInviteCode(code),
		Role:      role,
		MaxUses:   maxUses,
		CreatedBy: actor.ID,
		ExpiresAt: s.clock.Now().Add(ttl),
	}
	if err := s.invites.Create(ctx, inv); err != nil {
		return "", nil, err
	}
	s.writeAudit(ctx, actor.ID, "invite.create", "group", groupID,
		map[string]any{"invite_id": inv.ID, "role": string(role), "max_uses": maxUses})
	return code, inv, nil
}

// RedeemInvite — вступление по коду. Отозванный/истёкший код неотличим от
// несуществующего (ErrNotFound — не раскрываем существование); исчерпанный
// max_uses → ErrConflict; повторный redeem действующего участника идемпотентен
// (без инкремента used_count). Расход использования — атомарный
// IncrementUsed (условие max_uses в SQL), поэтому параллельные redeem не
// превышают лимит.
func (s *Service) RedeemInvite(ctx context.Context, actor *domain.User, code string) (*domain.Group, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	inv, err := s.invites.GetByCode(ctx, HashInviteCode(code))
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if inv.RevokedAt != nil || !inv.ExpiresAt.After(now) {
		return nil, fmt.Errorf("%w: invite expired or revoked", domain.ErrNotFound)
	}

	g, err := s.groups.GetByID(ctx, inv.GroupID)
	if err != nil {
		return nil, err
	}
	// Pending-группа не должна принимать чужих участников: тот же не-протекающий
	// ErrNotFound, что и в Get (видимость — создателю и superadmin).
	if g.Status != domain.GroupStatusActive && g.CreatedBy != actor.ID && !actor.IsSuperadmin {
		return nil, fmt.Errorf("%w: group id=%d is not active", domain.ErrNotFound, g.ID)
	}

	if _, err := s.members.Get(ctx, g.ID, actor.ID); err == nil {
		return g, nil // уже участник — идемпотентный успех, использование не тратим
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	// Сначала атомарно тратим использование: если ErrConflict — лимит исчерпан.
	// Обратный порядок (membership → increment) допускал бы overshoot в гонке.
	if err := s.invites.IncrementUsed(ctx, inv.ID); err != nil {
		return nil, err
	}
	m := &domain.Membership{GroupID: g.ID, UserID: actor.ID, Role: inv.Role}
	if err := s.members.Upsert(ctx, m); err != nil {
		// Сбой после расхода использования оставляет «потраченный» redeem —
		// приемлемо (inv.MaxUses — верхняя граница, не строгая квота).
		return nil, fmt.Errorf("groups: redeem membership: %w", err)
	}
	s.writeAudit(ctx, actor.ID, "invite.redeem", "group", g.ID,
		map[string]any{"invite_id": inv.ID, "role": string(inv.Role)})
	return g, nil
}

// RevokeInvite отзывает инвайт (admin). code — plaintext, как он был выдан.
func (s *Service) RevokeInvite(ctx context.Context, actor *domain.User, groupID int64, code string) error {
	if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return err
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if err := s.invites.Revoke(ctx, groupID, HashInviteCode(code)); err != nil {
		return err
	}
	s.writeAudit(ctx, actor.ID, "invite.revoke", "group", groupID, nil)
	return nil
}

// SetRole — promote/demote участника (admin). Понижение админа идёт через
// DemoteIfNotLastAdmin: защита «последнего админа» — в условном SQL репо,
// поэтому две конкурентные демotions не оставят группу без админа. Ограничение
// действует и для superadmin (группу нельзя «осиротить»).
func (s *Service) SetRole(ctx context.Context, actor *domain.User, groupID, userID int64, role domain.Role) error {
	if role != domain.RoleAdmin && role != domain.RoleMember {
		return &domain.ValidationError{Field: "role", Msg: "must be admin or member"}
	}
	if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return err
	}
	target, err := s.members.Get(ctx, groupID, userID)
	if err != nil {
		return err
	}
	if target.Role == domain.RoleAdmin && role == domain.RoleMember {
		if err := s.demoteGuarded(ctx, groupID, userID); err != nil {
			return err
		}
	} else if err := s.members.SetRole(ctx, groupID, userID, role); err != nil {
		return err
	}
	s.writeAudit(ctx, actor.ID, "member.set_role", "group", groupID,
		map[string]any{"user_id": userID, "role": string(role)})
	return nil
}

// RemoveMember — кик (admin). Админа может кикнуть только superadmin, но и он
// не может удалить последнего админа (группа не должна остаться без админа —
// вместо этого группу можно удалить).
func (s *Service) RemoveMember(ctx context.Context, actor *domain.User, groupID, userID int64) error {
	if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return err
	}
	target, err := s.members.Get(ctx, groupID, userID)
	if err != nil {
		return err
	}
	if target.Role == domain.RoleAdmin {
		if !actor.IsSuperadmin {
			return fmt.Errorf("%w: only superadmin can remove an admin", domain.ErrForbidden)
		}
		if err := s.removeGuarded(ctx, groupID, userID); err != nil {
			return err
		}
	} else if err := s.members.Delete(ctx, groupID, userID); err != nil {
		return err
	}
	s.writeAudit(ctx, actor.ID, "member.remove", "group", groupID, map[string]any{"user_id": userID})
	return nil
}

// Leave — выход из группы. Выход админа идёт через RemoveIfNotLastAdmin:
// последний админ выйти не может (ErrConflict + ErrLastAdmin), защита —
// в условном SQL репо, конкурентные выходы безопасны.
func (s *Service) Leave(ctx context.Context, actor *domain.User, groupID int64) error {
	m, err := s.members.Get(ctx, groupID, actor.ID)
	if err != nil {
		return err
	}
	if m.Role == domain.RoleAdmin {
		if err := s.removeGuarded(ctx, groupID, actor.ID); err != nil {
			return err
		}
	} else if err := s.members.Delete(ctx, groupID, actor.ID); err != nil {
		return err
	}
	s.writeAudit(ctx, actor.ID, "member.leave", "group", groupID, nil)
	return nil
}

// demoteGuarded — понижение с обёрткой ErrLastAdmin для HTTP-маппинга.
func (s *Service) demoteGuarded(ctx context.Context, groupID, userID int64) error {
	err := s.members.DemoteIfNotLastAdmin(ctx, groupID, userID)
	if errors.Is(err, domain.ErrConflict) {
		return fmt.Errorf("%w: %w: group id=%d user id=%d",
			domain.ErrConflict, ErrLastAdmin, groupID, userID)
	}
	return err
}

// removeGuarded — удаление с обёрткой ErrLastAdmin для HTTP-маппинга.
func (s *Service) removeGuarded(ctx context.Context, groupID, userID int64) error {
	err := s.members.RemoveIfNotLastAdmin(ctx, groupID, userID)
	if errors.Is(err, domain.ErrConflict) {
		return fmt.Errorf("%w: %w: group id=%d user id=%d",
			domain.ErrConflict, ErrLastAdmin, groupID, userID)
	}
	return err
}

// requireAdmin — роль admin в группе или superadmin; иначе ErrForbidden.
func (s *Service) requireAdmin(ctx context.Context, actor *domain.User, groupID int64) error {
	if actor.IsSuperadmin {
		if _, err := s.groups.GetByID(ctx, groupID); err != nil {
			return err
		}
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

// writeAudit — best-effort запись в audit_log: сбой аудита не роняет
// операцию (данные уже согласованы), но оставляет предупреждение в логе.
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
