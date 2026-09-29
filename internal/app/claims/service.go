// Package claims — use case выдачи первой роли Group Admin (спека §3.1):
// одноразовый 6-значный код публикуется в привязанном чате группы, участник
// чата вводит его в TMA и становится админом, после чего код сгорает. В БД
// хранится только SHA-256 кода. Лимиты/TTL — из конфига (спека §8).
//
// RULING (Task 10, лимит «3/час на чат»): таблица user_action_counters
// keyed by users.id (user_id REFERENCES users(id)), поэтому счётчик физически
// «на чат» не выражается. Лимиты считаются ПО ПОЛЬЗОВАТЕЛЮ:
//   - claim_request_hour — 3 запроса кода в час (LIMIT_CLAIM_PER_CHAT_HOUR);
//   - claim_cooldown — 1 запрос в минуту (спека §3.3, cooldown 1 мин).
//
// Инвариант «не более одного действующего кода на группу» обеспечивается
// отдельно: StartClaim отзывает предыдущий активный код группы.
package claims

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

var (
	// ErrNoBinding — у группы нет привязанного чата: код публиковать некуда.
	// Обёрнут в domain.ErrConflict (409): это состояние группы, а не отсутствие
	// объекта, и клиенту нужна подсказка «сначала /bind_group».
	ErrNoBinding = errors.New("group has no bound chat")
	// ErrCodeNotFound — нет действующего кода: не запрашивали, истёк или уже
	// погашен. «Истёк» и «нет» намеренно неразличимы (не раскрываем, был ли
	// код в чужом чате).
	ErrCodeNotFound = errors.New("no active claim code")
	// ErrWrongCode — хэш кода не совпал.
	ErrWrongCode = errors.New("wrong claim code")
	// ErrCodeSendFailed — код не удалось опубликовать в чате (бот кикнут/не
	// админ/429). Строка кода при этом НЕ создаётся.
	ErrCodeSendFailed = errors.New("claim code send failed")
)

// Config — лимиты и TTL из env (спека §8); нулевые значения заменяются
// дефолтами спеки в NewService.
type Config struct {
	// CodeTTL — CLAIM_CODE_TTL (дефолт 10m).
	CodeTTL time.Duration
	// RequestHourLimit — LIMIT_CLAIM_PER_CHAT_HOUR (дефолт 3); считается по
	// пользователю, см. RULING в описании пакета.
	RequestHourLimit int
	// Cooldown — пауза между запросами кода одного пользователя (1 минута).
	Cooldown time.Duration
}

// action-ключи счётчиков user_action_counters (PK: user_id, action,
// window_start). Action «claim_per_chat» как таковой невозможен — см. RULING.
const (
	actionRequestHour = "claim_request_hour"
	actionCooldown    = "claim_cooldown"
)

// requestWindow — окно лимита запросов кода.
const requestWindow = time.Hour

// Notifier — поверхность доставки, нужная claim-флоу. Реализуется
// internal/platform/telegram.Notifier: SendToChat возвращает message_id
// опубликованного сообщения (нужен для claim_codes.message_id).
type Notifier interface {
	SendToChat(ctx context.Context, chatID, threadID int64, text string) (messageID int64, err error)
	SendToUser(ctx context.Context, userID int64, text string) error
}

// Service — use cases claim-флоу; работает только с domain-портами.
type Service struct {
	groups   domain.GroupRepo
	members  domain.MembershipRepo
	bindings domain.ChatBindingRepo
	claims   domain.ClaimRepo
	counters domain.CounterRepo
	users    domain.UserRepo
	audit    domain.AuditRepo
	notifier Notifier
	cfg      Config
	clock    domain.Clock
	log      *slog.Logger
}

func NewService(
	groups domain.GroupRepo,
	members domain.MembershipRepo,
	bindings domain.ChatBindingRepo,
	claims domain.ClaimRepo,
	counters domain.CounterRepo,
	users domain.UserRepo,
	audit domain.AuditRepo,
	notifier Notifier,
	cfg Config,
	clock domain.Clock,
	log *slog.Logger,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	if cfg.CodeTTL <= 0 {
		cfg.CodeTTL = 10 * time.Minute
	}
	if cfg.RequestHourLimit <= 0 {
		cfg.RequestHourLimit = 3
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = time.Minute
	}
	return &Service{
		groups: groups, members: members, bindings: bindings, claims: claims,
		counters: counters, users: users, audit: audit, notifier: notifier,
		cfg: cfg, clock: clock, log: log,
	}
}

// StartResult — результат StartClaim для HTTP-ответа и логов.
type StartResult struct {
	ExpiresAt time.Time
	// ChatID — чат, куда опубликован код.
	ChatID int64
}

// HashCode — SHA-256 hex кода: в claim_codes.code_hash лежит только хэш
// (спека §4), plaintext существует лишь в сообщении чата и в ответе TMA.
func HashCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// GenerateCode — 6 цифр (000000..999999) на crypto/rand; ведущие нули
// сохраняются (код — строка, а не число).
func GenerateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("claims: generate code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// StartClaim — «Стать админом» (спека §3.1 шаг 3). Порядок:
// группа → привязка → участник → лимиты → админы «до» → генерация → публикация
// в чат → отзыв предыдущего кода → запись строки с message_id → аудит → ЛС.
//
// Вызывающий обязан быть участником группы (401 API): иначе любой
// авторизованный пользователь мог бы спамить кодом в чужой чат. Создатель
// группы и superadmin проходят всегда (см. requireMembership).
//
// Строка создаётся ТОЛЬКО после успешной отправки: message_id неизвестен
// заранее, а упавшая отправка не должна оставлять «активный» код, которого
// никто не видел. Предыдущий код гасится после успешной публикации нового —
// так сбой отправки не отнимает у чата последний действующий код.
//
// Группа с действующими админами не блокируется: это сценарий смены старосты
// (спека §3.1), админы получают уведомление и могут отозвать код.
func (s *Service) StartClaim(ctx context.Context, actor *domain.User, groupID int64) (*StartResult, error) {
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	b, err := s.bindings.GetByGroup(ctx, groupID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: %w", domain.ErrConflict, ErrNoBinding)
		}
		return nil, err
	}

	if err := s.requireMember(ctx, actor, g); err != nil {
		return nil, err
	}

	now := s.clock.Now()
	if err := s.checkLimits(ctx, actor.ID, now); err != nil {
		return nil, err
	}

	admins, err := s.adminTelegramIDs(ctx, groupID, actor.ID)
	if err != nil {
		return nil, err
	}

	code, err := GenerateCode()
	if err != nil {
		return nil, err
	}

	var threadID int64
	if b.MessageThreadID != nil {
		threadID = *b.MessageThreadID
	}
	messageID, err := s.notifier.SendToChat(ctx, b.ChatID, threadID,
		i18n.T("claim.code_message", code, humanTTL(s.cfg.CodeTTL)))
	if err != nil {
		s.log.Warn("claim: code send failed",
			slog.Int64("group_id", groupID),
			slog.Int64("chat_id", b.ChatID),
			slog.String("error", err.Error()),
		)
		// Настоящий 429 Telegram отдаётся как есть: у пользователя есть шанс
		// повторить, и HTTP-ответ должен быть 429 с Retry-After, а не 409.
		var rl *domain.RateLimitError
		if errors.As(err, &rl) && rl.RetryAfter > 0 {
			return nil, rl
		}
		return nil, fmt.Errorf("%w: %w: %w", domain.ErrConflict, ErrCodeSendFailed, err)
	}

	if err := s.claims.RevokeActiveByGroup(ctx, groupID, now); err != nil {
		return nil, err
	}

	cc := &domain.ClaimCode{
		GroupID:   groupID,
		CodeHash:  HashCode(code),
		ChatID:    b.ChatID,
		MessageID: messageID,
		CreatedBy: actor.ID,
		ExpiresAt: now.Add(s.cfg.CodeTTL),
	}
	if err := s.claims.Create(ctx, cc); err != nil {
		return nil, err
	}

	s.writeAudit(ctx, actor.ID, "claim.start", "group", groupID,
		map[string]any{"claim_id": cc.ID, "chat_id": b.ChatID, "admins": len(admins)})
	s.notifyAdmins(ctx, admins, i18n.T("claim.admin_changed", g.Slug))

	return &StartResult{ExpiresAt: cc.ExpiresAt, ChatID: b.ChatID}, nil
}

// Confirm — ввод кода в TMA (спека §3.1 шаг 4): сверка хэша constant-time,
// код сгорает, вызывающий получает роль admin, pending-группа становится
// active, действующие админы получают ЛС-уведомление. Участник мог ещё не
// вступить в группу — membership создаётся сразу с ролью admin.
func (s *Service) Confirm(ctx context.Context, actor *domain.User, groupID int64, code string) (*domain.Group, error) {
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	cc, err := s.claims.GetActiveByGroup(ctx, groupID, now)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: %w", domain.ErrNotFound, ErrCodeNotFound)
		}
		return nil, err
	}

	if subtle.ConstantTimeCompare([]byte(HashCode(code)), []byte(cc.CodeHash)) != 1 {
		return nil, fmt.Errorf("%w: %w", domain.ErrForbidden, ErrWrongCode)
	}

	// Код гасится ПЕРВЫМ вызовом условного UPDATE: точка сериализации гонки —
	// из двух одновременных Confirm успех получает ровно один, второй видит
	// ErrNotFound (claim_codes.used_at).
	if err := s.claims.MarkUsed(ctx, cc.ID, now); err != nil {
		return nil, err
	}

	admins, err := s.adminTelegramIDs(ctx, groupID, actor.ID)
	if err != nil {
		return nil, err
	}

	// Upsert создаёт membership (claim возможен до вступления в группу),
	// SetRole переводит существующего member в admin — Upsert роль не меняет.
	if err := s.members.Upsert(ctx, &domain.Membership{
		GroupID: groupID, UserID: actor.ID, Role: domain.RoleAdmin,
	}); err != nil {
		return nil, fmt.Errorf("claims: membership upsert: %w", err)
	}
	if err := s.members.SetRole(ctx, groupID, actor.ID, domain.RoleAdmin); err != nil {
		return nil, fmt.Errorf("claims: set admin role: %w", err)
	}

	if g.Status == domain.GroupStatusPending {
		if err := s.groups.SetStatus(ctx, groupID, domain.GroupStatusActive); err != nil {
			return nil, err
		}
		g.Status = domain.GroupStatusActive
	}

	s.writeAudit(ctx, actor.ID, "claim.confirm", "group", groupID,
		map[string]any{"claim_id": cc.ID, "role": string(domain.RoleAdmin)})
	s.notifyAdmins(ctx, admins, i18n.T("claim.admin_changed", g.Slug))
	// Подтвердившему — ЛС-подтверждение (claim.success, best-effort): результат
	// заметен, даже если TMA закрыт.
	s.notify(ctx, actor.TelegramID, i18n.T("claim.success", g.Title), "claimer")

	return g, nil
}

// Revoke — админ группы (или superadmin) гасит действующие коды: страховка от
// захвата при смене старосты (спека §3.1). Нет активного кода → ErrNotFound.
func (s *Service) Revoke(ctx context.Context, actor *domain.User, groupID int64) error {
	if err := s.requireAdmin(ctx, actor, groupID); err != nil {
		return err
	}
	now := s.clock.Now()
	if _, err := s.claims.GetActiveByGroup(ctx, groupID, now); err != nil {
		return err
	}
	if err := s.claims.RevokeActiveByGroup(ctx, groupID, now); err != nil {
		return err
	}
	s.writeAudit(ctx, actor.ID, "claim.revoke", "group", groupID, nil)
	return nil
}

// checkLimits — cooldown 1 мин и лимит запросов кода в час. Оба счётчика
// инкрементируются сразу (решение о превышении — по возвращённому count);
// окна floor-ятся на слое приложения, репо принимает window_start как есть.
func (s *Service) checkLimits(ctx context.Context, userID int64, now time.Time) error {
	cdStart := now.Truncate(s.cfg.Cooldown)
	count, err := s.counters.IncAndCheck(ctx, userID, actionCooldown, cdStart, 1)
	if err != nil {
		return fmt.Errorf("claims: counter %s: %w", actionCooldown, err)
	}
	if count > 1 {
		return &domain.RateLimitError{RetryAfter: s.cfg.Cooldown - now.Sub(cdStart)}
	}

	windowStart := now.Truncate(requestWindow)
	count, err = s.counters.IncAndCheck(ctx, userID, actionRequestHour, windowStart, s.cfg.RequestHourLimit)
	if err != nil {
		return fmt.Errorf("claims: counter %s: %w", actionRequestHour, err)
	}
	if count > s.cfg.RequestHourLimit {
		return &domain.RateLimitError{RetryAfter: requestWindow - now.Sub(windowStart)}
	}
	return nil
}

// adminTelegramIDs — telegram_id действующих админов группы, кроме skipUserID
// (себя уведомлять не нужно). Недостающий user (membership без строки users)
// пропускается: уведомление — best-effort.
func (s *Service) adminTelegramIDs(ctx context.Context, groupID, skipUserID int64) ([]int64, error) {
	mems, err := s.members.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(mems))
	for _, m := range mems {
		if m.Role != domain.RoleAdmin || m.UserID == skipUserID {
			continue
		}
		u, err := s.users.GetByID(ctx, m.UserID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, u.TelegramID)
	}
	return out, nil
}

// notifyAdmins — best-effort ЛС-уведомление действующих админов: сбой одной
// отправки (403 bot blocked, 429) логируется и не влияет на операцию и на
// остальных получателей.
func (s *Service) notifyAdmins(ctx context.Context, telegramIDs []int64, text string) {
	for _, tgID := range telegramIDs {
		s.notify(ctx, tgID, text, "admin")
	}
}

// notify — одна best-effort ЛС-отправка с логированием сбоя.
func (s *Service) notify(ctx context.Context, telegramID int64, text, kind string) {
	if err := s.notifier.SendToUser(ctx, telegramID, text); err != nil {
		s.log.Warn("claim: DM notify failed",
			slog.String("kind", kind),
			slog.Int64("telegram_id", telegramID),
			slog.String("error", err.Error()),
		)
	}
}

// requireMember — вызывающий состоит в группе (любая роль), он её создатель
// или superadmin; иначе ErrForbidden. Claim в pending-группу разрешён только
// «своим» (спека §3.1: группа без админов — для тех, кто в ней уже есть), а
// чужие группы становятся видны через инвайт (спека §3.2).
func (s *Service) requireMember(ctx context.Context, actor *domain.User, g *domain.Group) error {
	if actor.IsSuperadmin || g.CreatedBy == actor.ID {
		return nil
	}
	_, err := s.members.Get(ctx, g.ID, actor.ID)
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: group id=%d requires membership", domain.ErrForbidden, g.ID)
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

// writeAudit — best-effort: сбой аудита не роняет согласованную операцию.
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

// humanTTL — «10 минут» для текста сообщения (русские формы числителя).
func humanTTL(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "менее минуты"
	case d < time.Hour:
		n := int(d.Minutes())
		return fmt.Sprintf("%d %s", n, pluralRu(n, "минуту", "минуты", "минут"))
	default:
		n := int(d.Hours())
		return fmt.Sprintf("%d %s", n, pluralRu(n, "час", "часа", "часов"))
	}
}

func pluralRu(n int, one, few, many string) string {
	switch {
	case n%100 >= 11 && n%100 <= 14:
		return many
	case n%10 == 1:
		return one
	case n%10 >= 2 && n%10 <= 4:
		return few
	default:
		return many
	}
}
