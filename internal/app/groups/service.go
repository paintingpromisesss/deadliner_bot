// Package groups — use cases групп (спека §3, §5.2, §6.4): создание с
// нормализацией и валидацией слага, антиспам-лимиты (3/сутки, 5/неделю),
// поиск, membership-операции и инвайт-коды.
package groups

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// ErrLastAdmin — маркер конфликтов «последний админ группы» (обёрнут в
// domain.ErrConflict): понижение, кик или выход единственного админа.
var ErrLastAdmin = errors.New("last admin: transfer admin role first")

// Сентинелы привязки чата (спека §6.1, /bind_group): несут точный текст для
// пользователя, поэтому различаются, хотя оба — конфликты.
var (
	// ErrChatAlreadyBound — чат (или топик) уже привязан к другой группе:
	// инструкция — /unbind в том чате.
	ErrChatAlreadyBound = errors.New("chat already bound to another group")
	// ErrGroupAlreadyBound — у группы уже есть другой чат.
	ErrGroupAlreadyBound = errors.New("group already bound to another chat")
	// ErrNotMember — вызывающий не участник группы (и не её создатель).
	ErrNotMember = errors.New("actor is not a member of the group")
	// ErrBindingNotFound — у чата нет привязки (/unbind нечего снимать).
	ErrBindingNotFound = errors.New("chat is not bound to any group")
	// ErrNoBinding — у группы нет привязанного чата: инвайт публиковать
	// некуда (чекбокс «Опубликовать в чат» недоступен до /bind_group).
	ErrNoBinding = errors.New("group has no bound chat")
	// ErrPublishFailed — инвайт создан, но сообщение в чат не доставлено
	// (бот кикнут/нет прав/лимит Telegram).
	ErrPublishFailed = errors.New("invite publish failed")
)

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
	// slugs — валидатор слага (domain.SlugProvider). nil означает «без
	// провайдера»: в этом случае Create валидирует доменным
	// ValidateStrict (структура + цифра) — так собираются тесты, которым
	// провайдер не нужен. serve всегда передаёт local-провайдер поверх
	// SLUG_REGEX, поэтому на проде charset приходит из конфига.
	slugs domain.SlugProvider
	// notifier — ЛС супер-админам (/report_slug, спека §3.3). nil отключает
	// жалобу: без транспорта её некуда доставлять.
	notifier UserNotifier
	// users — адресаты жалоб (ListSuperadmins). nil отключает /report_slug.
	users domain.UserRepo
	// invitePublisher — публикация инвайтов в чат группы. nil → CreateInvite
	// с publish=true отвечает ошибкой (нет транспорта).
	invitePublisher ChatPublisher
	cfg             Config
	clock           domain.Clock
	log             *slog.Logger
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

// UserNotifier — узкая поверхность доставки ЛС. Реализуется
// telegram.Notifier (domain.Notifier); сервису групп нужен ровно один метод —
// жалоба на слаг уходит супер-админам (спека §3.3, /report_slug).
type UserNotifier interface {
	SendToUser(ctx context.Context, userID int64, text string) error
}

// ChatPublisher — публикация инвайт-сообщения в привязанный чат группы
// (кнопка «Опубликовать в чат» при создании инвайта). Реализуется
// telegram.InvitePublisher; интерфейс объявлен здесь, чтобы app не зависел
// от platform.
type ChatPublisher interface {
	// inviteCode — plaintext-код инвайта: нужен для Main App direct-link
	// (startapp-параметр) на кнопке сообщения.
	PublishInvite(ctx context.Context, chatID, threadID int64, text, inviteCode string) error
}

// Options — необязательные зависимости сервиса: часть сборок (unit-тесты use
// case) обходится без провайдера слага и без нотификатора, поэтому они не
// входят в конструктор. serve задаёт обе.
type Options struct {
	// Slugs — валидатор/подсказка слага (domain.SlugProvider, спека §2.2).
	// nil → Create валидирует доменными правилами, Search идёт напрямую в репо.
	Slugs domain.SlugProvider
	// Notifier — доставка ЛС супер-админам для /report_slug. nil → жалоба
	// вернёт ошибку (без транспорта отправить её нечем).
	Notifier UserNotifier
	// Users — источник адресатов жалобы (ListSuperadmins). nil → жалоба
	// недоступна.
	Users domain.UserRepo
	// InvitePublisher — публикация инвайт-сообщений в чат группы. nil →
	// CreateInvite отвергает publish=true (нет транспорта).
	InvitePublisher ChatPublisher
}

// WithOptions подключает необязательные зависимости. Отдельный шаг сборки, а
// не параметр конструктора: вызовов NewService шесть, и большинству из них
// провайдер и нотификатор не нужны.
func (s *Service) WithOptions(o Options) *Service {
	s.slugs = o.Slugs
	s.notifier = o.Notifier
	s.users = o.Users
	s.invitePublisher = o.InvitePublisher
	return s
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

// Create создаёт pending-группу с антиспам-лимитами §3.3. Создатель
// становится АДМИНОМ группы сразу (клейм-коды удалены): активация группы —
// привязка чата (/bind_group). Порядок: нормализация → валидация слага →
// лимиты → группа → membership создателя (admin) → аудит.
//
// Валидация идёт через domain.SlugProvider (спека §2.2): SLUG_REGEX из
// конфига отвечает за charset, провайдер добавляет правила Deadliner
// (длина/сегменты/цифра). Superadmin — обходной путь: он создаёт группы вне
// формата (спека §3.3), поэтому провайдер к нему не применяется.
func (s *Service) Create(ctx context.Context, actor *domain.User, slugRaw, title string) (*domain.Group, error) {
	slug := domain.Normalize(slugRaw)
	if !actor.IsSuperadmin {
		if err := s.validateSlug(slug); err != nil {
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

	m := &domain.Membership{GroupID: g.ID, UserID: actor.ID, Role: domain.RoleAdmin}
	if err := s.members.Upsert(ctx, m); err != nil {
		return nil, fmt.Errorf("groups: creator membership: %w", err)
	}

	s.writeAudit(ctx, actor.ID, "group.create", "group", g.ID, map[string]any{"slug": g.Slug})
	return g, nil
}

// ReportSlug — жалоба админа группы на конфликтующий слаг (спека §3.3,
// «Жалобы»): админ пишет в ЛС боту /report_slug <slug>, и жалоба уходит в ЛС
// ВСЕМ супер-админам.
//
// Права: вызывающий обязан быть админом СУЩЕСТВУЮЩЕЙ группы (любого статуса —
// pending тоже: конфликт слага возникает до активации). Условие «слаг
// конфликтует» намеренно НЕ проверяется: конфликт вузовских слагов — это
// спор о принадлежности, который разрешает человек, а не бот; техническая
// проверка «занят/свободен» только мешала бы законным жалобам.
//
// Не-админ и неизвестный слаг получают одинаковый ответ (ErrForbidden /
// ErrNotFound) — по нему нельзя узнать, существует ли слаг. Отправка
// best-effort: сбой ЛС одному супер-админу не отменяет остальных, но
// фиксируется в логе; аудит пишется всегда (жалоба = факт обращения).
func (s *Service) ReportSlug(ctx context.Context, actor *domain.User, slug string) (*domain.Group, error) {
	if s.users == nil || s.notifier == nil {
		return nil, fmt.Errorf("%w: slug reports are not wired", domain.ErrForbidden)
	}
	norm := domain.Normalize(slug)
	g, err := s.groups.GetBySlugNorm(ctx, norm)
	if err != nil {
		return nil, err
	}
	// Жаловаться может админ группы (любой статус) или superadmin.
	if err := s.requireAdmin(ctx, actor, g.ID); err != nil {
		return nil, err
	}

	admins, err := s.users.ListSuperadmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("groups: list superadmins: %w", err)
	}

	reporter := reporterName(actor)
	// Порядок аргументов — строго по шаблону superadmin.slug_report:
	// слаг / id группы / отправитель. Перестановка здесь давала супер-админам
	// «Группа: id=Иван / Отправитель: 7» (номер группы уезжал в имя).
	text := i18n.T("superadmin.slug_report",
		i18n.EscapeHTML(g.Slug), formatInt64(g.ID), i18n.EscapeHTML(reporter))
	delivered := 0
	for _, sa := range admins {
		if sa.BotBlocked {
			// Пользователь не писал боту или заблокировал его: отправка
			// гарантированно даст 403 (§7.3) — пропускаем с записью в лог.
			s.log.Warn("slug report: superadmin blocked the bot",
				slog.Int64("superadmin_id", sa.ID))
			continue
		}
		if err := s.notifier.SendToUser(ctx, sa.TelegramID, text); err != nil {
			s.log.Warn("slug report: delivery failed",
				slog.Int64("superadmin_id", sa.ID), slog.String("error", err.Error()))
			continue
		}
		delivered++
	}
	if delivered == 0 {
		s.log.Warn("slug report: no superadmin received the complaint",
			slog.Int64("group_id", g.ID), slog.Int("superadmins", len(admins)))
	}

	s.writeAudit(ctx, actor.ID, "slug.report", "group", g.ID, map[string]any{
		"slug": g.Slug, "delivered": delivered, "superadmins": len(admins),
	})
	return g, nil
}

// reporterName — как подписать жалобу: имя, а при пустом — username, иначе
// telegram_id. Пользователь, писавший боту, почти всегда имеет first_name,
// но подставлять пустую строку в текст нельзя.
func reporterName(u *domain.User) string {
	if u == nil {
		return ""
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return formatInt64(u.TelegramID)
}

// formatInt64 — число для текста каталога.
func formatInt64(n int64) string { return strconv.FormatInt(n, 10) }

// validateSlug — валидация через подключённый провайдер, а при его
// отсутствии — доменными правилами (структура + цифра, без charset: он
// принадлежит SLUG_REGEX). Второй путь существует только для сборок без
// провайдера (тесты use case); serve провайдер задаёт всегда.
func (s *Service) validateSlug(slug string) error {
	if s.slugs != nil {
		return s.slugs.Validate(slug)
	}
	return domain.ValidateStrict(slug)
}

// checkCreateLimit инкрементирует счётчик окна и отклоняет превышение.
// Начало окна floor-ится на слое приложения (репо принимает его как есть).
//
// ВНИМАНИЕ (связь с cleanup): окно floor-ится на своё начало, поэтому у
// недельного счётчика (168ч) window_start бывает почти 168 часов от роду.
// Уборка счётчиков (moderation.CleanupExpiredPending) обязана иметь ретенцию
// строго больше 168ч (COUNTER_RETENTION, дефолт 192ч, проверяется в
// config.Load): иначе cleanup удалял бы ЖИВУЮ строку недельного лимита и
// LIMIT_GROUP_CREATE_WEEK молча перестал бы срабатывать.
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
//
// Подсказки берутся у SlugProvider (спека §2.2) — это его Suggest-часть
// (спека §6.4 через локальную таблицу); при отсутствии провайдера — прямой
// GroupRepo.SearchByPrefix, чтобы сборки без провайдера работали как раньше.
func (s *Service) Search(ctx context.Context, actor *domain.User, q string) ([]MyGroup, error) {
	found, err := s.suggest(ctx, actor.ID, q)
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

// suggest — префиксный поиск через провайдера или напрямую.
func (s *Service) suggest(ctx context.Context, callerID int64, q string) ([]domain.Group, error) {
	if s.slugs != nil {
		return s.slugs.Suggest(ctx, q, callerID, SearchLimit)
	}
	return s.groups.SearchByPrefix(ctx, domain.Normalize(q), callerID, SearchLimit)
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

// maxInviteTTL — верхняя граница TTL инвайта (90 дней). TTL ≤ 0 — бессрочный
// инвайт (expires_at IS NULL: действует, пока не отозван и не исчерпан лимит).
const maxInviteTTL = 90 * 24 * time.Hour

// CreateInvite генерирует инвайт-код (admin). maxUses: -1 = без лимита,
// ≥1 — число использований; 0 и < -1 — ErrValidation. ttl == 0 → бессрочный
// (expires_at IS NULL); ttl < 0 → дефолт из конфига; ttl > 90 дней —
// ErrValidation. publish=true отправляет сообщение в привязанный чат группы
// (без чата — ErrConflict с точной подсказкой; публикация — best-effort
// ПОСЛЕ сохранения кода: сбой отправки не отменяет инвайт, но возвращает
// ошибку, чтобы вызывающий не считал чат оповещённым). Возвращает
// plaintext-код (показывается один раз) и сохранённый инвайт с хэшем.
func (s *Service) CreateInvite(ctx context.Context, actor *domain.User, groupID int64, role domain.Role, maxUses int, ttl time.Duration, publish bool) (string, *domain.Invite, error) {
	if role != domain.RoleAdmin && role != domain.RoleMember {
		return "", nil, &domain.ValidationError{Field: "role", Msg: "must be admin or member"}
	}
	if maxUses == 0 || maxUses < -1 {
		return "", nil, &domain.ValidationError{Field: "max_uses", Msg: "must be -1 (unlimited) or >= 1"}
	}
	if ttl > maxInviteTTL {
		return "", nil, &domain.ValidationError{Field: "ttl_hours", Msg: "must be within 0..2160 hours"}
	}

	var binding *domain.ChatBinding
	if publish {
		if err := s.requireAdmin(ctx, actor, groupID); err != nil {
			return "", nil, err
		}
		b, err := s.bindings.GetByGroup(ctx, groupID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return "", nil, fmt.Errorf("%w: %w", domain.ErrConflict, ErrNoBinding)
			}
			return "", nil, err
		}
		binding = b
	} else if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return "", nil, err
	}

	// ttl < 0 — «дефолт из конфига» для обратной совместимости вызовов
	// без явного TTL; ttl == 0 — бессрочный инвайт.
	var expiresAt time.Time
	switch {
	case ttl < 0:
		ttl = s.cfg.InviteDefaultTTL
		expiresAt = s.clock.Now().Add(ttl)
	case ttl == 0:
		expiresAt = time.Time{} // NULL в БД
	default:
		expiresAt = s.clock.Now().Add(ttl)
	}

	code, err := generateInviteCode()
	if err != nil {
		return "", nil, err
	}
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return "", nil, err
	}
	inv := &domain.Invite{
		GroupID:   groupID,
		Code:      HashInviteCode(code),
		Role:      role,
		MaxUses:   maxUses,
		CreatedBy: actor.ID,
		ExpiresAt: expiresAt,
	}
	if err := s.invites.Create(ctx, inv); err != nil {
		return "", nil, err
	}
	s.writeAudit(ctx, actor.ID, "invite.create", "group", groupID,
		map[string]any{"invite_id": inv.ID, "role": string(role), "max_uses": maxUses})

	if publish && binding != nil {
		var threadID int64
		if binding.MessageThreadID != nil {
			threadID = *binding.MessageThreadID
		}
		text := i18n.T("invite.chat_message", i18n.EscapeHTML(g.Title))
		if err := s.invitePublisher.PublishInvite(ctx, binding.ChatID, threadID, text, code); err != nil {
			// Код уже сохранён и действующ: сбой публикации не отменяет его,
			// но вызывающий должен знать, что чат не оповещён.
			s.log.Warn("groups: invite publish failed",
				slog.Int64("group_id", groupID),
				slog.Int64("chat_id", binding.ChatID),
				slog.String("error", err.Error()))
			return code, inv, fmt.Errorf("%w: %w: %w", domain.ErrConflict, ErrPublishFailed, err)
		}
	}
	return code, inv, nil
}

// RedeemInvite — вступление по коду. Отозванный/истёкший код неотличим от
// несуществующего (ErrNotFound — не раскрываем существование); исчерпанный
// max_uses → ErrConflict; повторный redeem действующего участника идемпотентен
// (без инкремента used_count). Бессрочный код (expires_at NULL) истекает
// только по отзыву/лимиту. Расход использования — атомарный IncrementUsed
// (условие max_uses в SQL), поэтому параллельные redeem не превышают лимит.
// Списание строго по факту успешной привязки: «Отмена»/закрытие окна
// лимит не тратит.
func (s *Service) RedeemInvite(ctx context.Context, actor *domain.User, code string) (*domain.Group, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	inv, err := s.invites.GetByCode(ctx, HashInviteCode(code))
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if inv.RevokedAt != nil || !inv.ExpiresAtValid(now) {
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

// InvitePreview — данные экрана подтверждения «Вступить в группу?» при
// открытии Mini App по startapp-параметру: название группы и её слаг, БЕЗ
// расхода лимита (использование тратится только кнопкой «Вступить»).
// Неизвестный/отозванный/истёкший код → ErrNotFound (без раскрытия деталей).
func (s *Service) InvitePreview(ctx context.Context, code string) (*domain.Group, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	inv, err := s.invites.GetByCode(ctx, HashInviteCode(code))
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if inv.RevokedAt != nil || !inv.ExpiresAtValid(now) {
		return nil, fmt.Errorf("%w: invite expired or revoked", domain.ErrNotFound)
	}
	g, err := s.groups.GetByID(ctx, inv.GroupID)
	if err != nil {
		return nil, err
	}
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

// BindChat привязывает чат (или топик форума) к группе по слагу (спека §6.1,
// /bind_group). Проверки: слаг существует, вызывающий — участник группы (её
// создатель считается участником: membership создаётся при Create), 1 чат =
// 1 группа и 1 группа = 1 чат (уникальные индексы chat_bindings → ErrConflict
// с точной подсказкой). Проверка «бот — админ чата» выполняется в бот-хендлере
// через Telegram API (ChatAdminChecker) и сюда не входит: use case не знает
// про Telegram.
//
// Привязка чата АКТИВИРУЕТ pending-группу: создатель уже админ (роль выдаётся
// при Create), клейм-кодов больше нет. Повторная привязка активной группы
// статус не меняет.
func (s *Service) BindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64, slug, chatTitle string) (*domain.Group, error) {
	g, err := s.groups.GetBySlugNorm(ctx, domain.Normalize(slug))
	if err != nil {
		return nil, err
	}

	if err := s.requireMembership(ctx, actor, g); err != nil {
		return nil, err
	}

	b := &domain.ChatBinding{
		GroupID:         g.ID,
		ChatID:          chatID,
		MessageThreadID: threadID,
		ChatTitle:       chatTitle,
		BoundBy:         actor.ID,
	}
	if err := s.bindings.Create(ctx, b); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, s.classifyBindConflict(ctx, g.ID, chatID, threadID)
		}
		return nil, err
	}

	if g.Status == domain.GroupStatusPending {
		if err := s.groups.SetStatus(ctx, g.ID, domain.GroupStatusActive); err != nil {
			return nil, err
		}
		g.Status = domain.GroupStatusActive
	}

	s.writeAudit(ctx, actor.ID, "chat.bind", "group", g.ID,
		map[string]any{"chat_id": chatID, "thread_id": threadID})
	return g, nil
}

// classifyBindConflict различает два уникальных индекса chat_bindings: у
// пользователя должны быть разные подсказки на «этот чат занят другой группой»
// (→ /unbind здесь) и «эта группа занята другим чатом». Групповой индекс
// проверяется первым: он уникален, а (chat_id, thread_id) может быть занят
// той же самой строкой.
func (s *Service) classifyBindConflict(ctx context.Context, groupID, chatID int64, threadID *int64) error {
	if _, err := s.bindings.GetByGroup(ctx, groupID); err == nil {
		return fmt.Errorf("%w: %w", domain.ErrConflict, ErrGroupAlreadyBound)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if _, err := s.bindings.GetByChat(ctx, chatID, threadID); err == nil {
		return fmt.Errorf("%w: %w", domain.ErrConflict, ErrChatAlreadyBound)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	// Обе строки исчезли между INSERT и чтением (unbind в гонке) — общий конфликт.
	return fmt.Errorf("%w: chat binding race", domain.ErrConflict)
}

// UnbindChat снимает привязку чата. Требует роль admin группы (или
// superadmin): иначе привязку в общем чате снял бы любой его участник.
// Группа опознаётся по (chat_id, thread_id) — команде /unbind известен только
// чат и топик.
func (s *Service) UnbindChat(ctx context.Context, actor *domain.User, chatID int64, threadID *int64) (*domain.Group, error) {
	b, err := s.bindings.GetByChat(ctx, chatID, threadID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: %w", domain.ErrNotFound, ErrBindingNotFound)
		}
		return nil, err
	}
	if err := s.requireAdmin(ctx, actor, b.GroupID); err != nil {
		return nil, err
	}
	if err := s.bindings.Delete(ctx, b.GroupID); err != nil {
		return nil, err
	}
	s.writeAudit(ctx, actor.ID, "chat.unbind", "group", b.GroupID,
		map[string]any{"chat_id": chatID, "thread_id": threadID})
	g, err := s.groups.GetByID(ctx, b.GroupID)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// BindingByChat — привязка чата/топика (бот-командам нужна группа по чату).
// Отсутствие привязки → ErrBindingNotFound.
func (s *Service) BindingByChat(ctx context.Context, chatID int64, threadID *int64) (*domain.ChatBinding, error) {
	b, err := s.bindings.GetByChat(ctx, chatID, threadID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: %w", domain.ErrNotFound, ErrBindingNotFound)
		}
		return nil, err
	}
	return b, nil
}

// requireMembership — вызывающий состоит в группе (любая роль), он её создатель
// или superadmin. Создатель сохраняет право привязки и после выхода из
// membership: привязка чата — часть онбординга созданной им группы.
func (s *Service) requireMembership(ctx context.Context, actor *domain.User, g *domain.Group) error {
	if actor.IsSuperadmin || g.CreatedBy == actor.ID {
		return nil
	}
	_, err := s.members.Get(ctx, g.ID, actor.ID)
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: %w: group id=%d", domain.ErrForbidden, ErrNotMember, g.ID)
	}
	return err
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
