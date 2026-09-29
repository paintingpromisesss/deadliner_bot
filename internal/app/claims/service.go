// Package claims — use case выдачи первой роли Group Admin (спека §3.1):
// одноразовый 6-значный код публикуется в привязанном чате группы, участник
// чата вводит его в TMA и становится админом, после чего код сгорает. В БД
// хранится только SHA-256 кода. Лимиты/TTL — из конфига (спека §8).
//
// Лимиты (спека §3.3):
//   - claim_chat_hour — 3 запроса кода в час НА ЧАТ (LIMIT_CLAIM_PER_CHAT_HOUR,
//     chat_action_counters: основное требование спеки, чат — единица учёта);
//   - claim_request_hour — те же 3/час на пользователя (user_action_counters)
//     как defense-in-depth против рассылки по чужим чатам;
//   - claim_cooldown — 1 запрос в минуту на пользователя;
//   - claim_confirm_fail — не более 10 неверных вводов кода на пользователя за
//     окно CLAIM_CODE_TTL: 6-значный код без ограничения попыток перебирается
//     быстро, поэтому счётчик проверяется ДО сверки хэша, а исчерпание лимита
//     гасит активный код группы (окно закрывается).
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
	"strings"
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
	// RequestHourLimit — LIMIT_CLAIM_PER_CHAT_HOUR (дефолт 3). Основной лимит
	// считается НА ЧАТ (спека §3.3) через ChatCounterRepo; тот же предел
	// дополнительно применяется на пользователя как defense-in-depth.
	RequestHourLimit int
	// Cooldown — пауза между запросами кода одного пользователя (1 минута).
	Cooldown time.Duration
	// ConfirmFailLimit — максимум неверных вводов кода на пользователя за
	// окно ConfirmFailWindow (дефолт 10 за CLAIM_CODE_TTL): 6-значный код без
	// ограничения попыток перебирается за минуты.
	ConfirmFailLimit int
	// ConfirmFailWindow — окно лимита неверных вводов (дефолт = CodeTTL, 10m).
	ConfirmFailWindow time.Duration
}

// action-ключи счётчиков. user_action_counters — по users.id (PK: user_id,
// action, window_start); chat_action_counters — по chat_id (спека §3.3:
// 3 claim-кода в час именно НА ЧАТ), поэтому у лимита запроса кода два
// независимых счётчика: per-chat (основной) и per-user (defense-in-depth).
const (
	actionRequestHourChat = "claim_chat_hour"
	actionRequestHourUser = "claim_request_hour"
	actionCooldown        = "claim_cooldown"
	actionConfirmFail     = "claim_confirm_fail"
)

// requestWindow — окно лимита запросов кода (1 час, спека §3.3).
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
	groups      domain.GroupRepo
	members     domain.MembershipRepo
	bindings    domain.ChatBindingRepo
	claims      domain.ClaimRepo
	counters    domain.CounterRepo
	chatCounter domain.ChatCounterRepo
	users       domain.UserRepo
	audit       domain.AuditRepo
	notifier    Notifier
	cfg         Config
	clock       domain.Clock
	log         *slog.Logger
}

// NewService собирает claim-сервис. chatCounters — счётчики НА ЧАТ (спека
// §3.3); nil допустим только для совместимости с уже собранными тестами, но
// тогда per-chat лимит не действует — serve обязан передать repo.NewChatCounters.
func NewService(
	groups domain.GroupRepo,
	members domain.MembershipRepo,
	bindings domain.ChatBindingRepo,
	claims domain.ClaimRepo,
	counters domain.CounterRepo,
	chatCounters domain.ChatCounterRepo,
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
	if cfg.ConfirmFailLimit <= 0 {
		cfg.ConfirmFailLimit = 10
	}
	if cfg.ConfirmFailWindow <= 0 {
		cfg.ConfirmFailWindow = cfg.CodeTTL
	}
	return &Service{
		groups: groups, members: members, bindings: bindings, claims: claims,
		counters: counters, chatCounter: chatCounters,
		users: users, audit: audit, notifier: notifier,
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
	if err := s.checkLimits(ctx, actor.ID, b.ChatID, now); err != nil {
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
		// Код уже опубликован в чате, но строки нет: участники видят код,
		// который невозможно подтвердить. Отдельная запись в аудите и Error
		// в лог — иначе случай неотличим от «код не запрашивали».
		s.log.Error("claim: code published but not persisted",
			slog.Int64("group_id", groupID), slog.Int64("chat_id", b.ChatID),
			slog.String("error", err.Error()))
		s.writeAudit(ctx, actor.ID, "claim.publish_orphan", "group", groupID,
			map[string]any{"chat_id": b.ChatID, "message_id": messageID})
		return nil, err
	}

	s.writeAudit(ctx, actor.ID, "claim.start", "group", groupID,
		map[string]any{"claim_id": cc.ID, "chat_id": b.ChatID, "admins": len(admins)})
	// «Запрошена смена»: код только что опубликован, подтверждения ещё не было
	// — админам нужен шанс отозвать код (спека §3.1), а не уведомление о факте.
	s.notifyAdmins(ctx, admins, i18n.T("claim.admin_change_started", i18n.EscapeHTML(g.Title)))

	return &StartResult{ExpiresAt: cc.ExpiresAt, ChatID: b.ChatID}, nil
}

// Confirm — ввод кода в TMA (спека §3.1 шаг 4): бюджет неверных попыток,
// сверка хэша constant-time, код сгорает, вызывающий получает роль admin,
// pending-группа становится active, действующие админы получают ЛС-уведомление.
// Участник мог ещё не вступить в группу — membership создаётся сразу с ролью admin.
//
// Порядок важен: бюджет попыток проверяется ДО сверки хэша и ДО поиска кода,
// поэтому перебор не получает бесплатных попыток и не зависит от того, есть ли
// активный код.
func (s *Service) Confirm(ctx context.Context, actor *domain.User, groupID int64, code string) (*domain.Group, error) {
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	if err := s.checkConfirmBudget(ctx, actor.ID, groupID, now); err != nil {
		return nil, err
	}

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
	// Смена состоялась: имя нового админа в тексте — first_name, иначе @username.
	s.notifyAdmins(ctx, admins, i18n.T("claim.admin_replaced",
		i18n.EscapeHTML(g.Title), i18n.EscapeHTML(displayName(actor))))
	// Подтвердившему — ЛС-подтверждение (claim.success, best-effort): результат
	// заметен, даже если TMA закрыт.
	s.notify(ctx, actor.TelegramID, i18n.T("claim.success", i18n.EscapeHTML(g.Title)), "claimer")

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

// checkLimits — cooldown 1 мин и лимит запросов кода в час НА ЧАТ (спека
// §3.3, chat_action_counters) плюс тот же предел на пользователя как
// defense-in-depth. Все счётчики инкрементируются сразу, решение о превышении
// принимается по возвращённому count; окна floor-ятся на слое приложения, репо
// принимает window_start как есть.
func (s *Service) checkLimits(ctx context.Context, actorID, chatID int64, now time.Time) error {
	cdStart := now.Truncate(s.cfg.Cooldown)
	count, err := s.counters.IncAndCheck(ctx, actorID, actionCooldown, cdStart, 1)
	if err != nil {
		return fmt.Errorf("claims: counter %s: %w", actionCooldown, err)
	}
	if count > 1 {
		return &domain.RateLimitError{RetryAfter: s.cfg.Cooldown - now.Sub(cdStart)}
	}

	windowStart := now.Truncate(requestWindow)
	// Основной лимит — на чат: он держит спам даже при смене злоумышленником
	// аккаунтов, чего per-user счётчик не умеет.
	if s.chatCounter != nil {
		count, err = s.chatCounter.IncAndCheck(ctx, chatID, actionRequestHourChat, windowStart, s.cfg.RequestHourLimit)
		if err != nil {
			return fmt.Errorf("claims: counter %s: %w", actionRequestHourChat, err)
		}
		if count > s.cfg.RequestHourLimit {
			return &domain.RateLimitError{RetryAfter: requestWindow - now.Sub(windowStart)}
		}
	}

	count, err = s.counters.IncAndCheck(ctx, actorID, actionRequestHourUser, windowStart, s.cfg.RequestHourLimit)
	if err != nil {
		return fmt.Errorf("claims: counter %s: %w", actionRequestHourUser, err)
	}
	if count > s.cfg.RequestHourLimit {
		return &domain.RateLimitError{RetryAfter: requestWindow - now.Sub(windowStart)}
	}
	return nil
}

// checkConfirmBudget — бюджет неверных вводов кода на пользователя (окно
// ConfirmFailWindow). Проверяется ДО сверки хэша: иначе перебор 6-значного кода
// ограничивался бы только пропускной способностью API.
//
// ЗАМЕЧАНИЕ по реализации: порт CounterRepo умеет только IncAndCheck
// (инкремент + чтение), отдельного read-only чтения в домене нет, поэтому
// бюджет расходует ЛЮБАЯ попытка — и неудачная, и успешная. Это строго
// консервативнее требования «инкрементировать на каждой неудачной попытке»:
// успешный ввод кода стоит одной попытки из бюджета, зато проверка остаётся
// строго до сверки хэша. На практике успешный claim бывает один на код, а
// бюджет — 10 попыток за окно TTL кода.
//
// Исчерпание бюджета гасит активный код группы ТОЛЬКО если исчерпавший —
// участник группы: посторонний, дожигая свой личный бюджет, иначе мог бы
// вынуждать админов выпускать код заново бесконечно (DoS на смену старосты).
// Не-участник получает лишь собственный RateLimitError, код остаётся живым —
// он всё равно не видит код чата (социальный контроль, спека §3.1).
func (s *Service) checkConfirmBudget(ctx context.Context, actorID, groupID int64, now time.Time) error {
	windowStart := now.Truncate(s.cfg.ConfirmFailWindow)
	count, err := s.counters.IncAndCheck(ctx, actorID, actionConfirmFail, windowStart, s.cfg.ConfirmFailLimit)
	if err != nil {
		return fmt.Errorf("claims: counter %s: %w", actionConfirmFail, err)
	}
	if count <= s.cfg.ConfirmFailLimit {
		return nil
	}

	rateLimited := &domain.RateLimitError{RetryAfter: s.cfg.ConfirmFailWindow - now.Sub(windowStart)}

	member, err := s.isMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if !member {
		// Посторонний исчерпал свой бюджет попыток: ограничиваем его самого,
		// но НЕ трогаем код группы.
		s.log.Info("claims: confirm budget exhausted by a non-member",
			slog.Int64("group_id", groupID), slog.Int64("user_id", actorID))
		return rateLimited
	}

	// Участник группы: гасим действующий код, окно перебора закрывается вместе
	// с кодом (счётчик — на пользователя, поэтому сам код и есть общий барьер).
	if cc, err := s.claims.GetActiveByGroup(ctx, groupID, now); err == nil {
		if err := s.claims.MarkUsed(ctx, cc.ID, now); err != nil {
			s.log.Warn("claims: burning bruteforced code failed",
				slog.Int64("group_id", groupID), slog.Int64("claim_id", cc.ID),
				slog.String("error", err.Error()))
		} else {
			s.writeAudit(ctx, actorID, "claim.code_burned", "group", groupID,
				map[string]any{"claim_id": cc.ID, "reason": "confirm_attempts_exhausted"})
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return rateLimited
}

// displayName — человекочитаемое имя пользователя для уведомлений о смене
// старосты: first_name, иначе @username, иначе нейтральное «участник».
// Экранируется вызывающей стороной (тексты уходят с parse_mode=HTML).
func displayName(u *domain.User) string {
	if u == nil {
		return "участник"
	}
	if strings.TrimSpace(u.FirstName) != "" {
		return u.FirstName
	}
	if strings.TrimSpace(u.Username) != "" {
		return "@" + u.Username
	}
	return "участник"
}

// isMember — есть ли у пользователя membership группы (ровно то, что вернул бы
// MembershipRepo.Get). Создатель, вышедший из группы, и superadmin сюда не
// попадают: право «сжечь» код намеренно минимально (правило ревью — гаснуть
// код может только для участника), ошибиться в сторону «не жечь» безопасно.
func (s *Service) isMember(ctx context.Context, groupID, userID int64) (bool, error) {
	if _, err := s.members.Get(ctx, groupID, userID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
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

// humanTTL — «10 минут» для текста сообщения. Формы числителя — общий хелпер
// i18n.Plural (тот же, что использует scheduler: логика больше не дублируется).
func humanTTL(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "менее минуты"
	case d < time.Hour:
		n := int(d.Minutes())
		if n == 0 {
			n = 1
		}
		return fmt.Sprintf("%d %s", n, i18n.Plural(n, "минуту", "минуты", "минут"))
	case d <= 24*time.Hour:
		n := int(d.Hours())
		if n == 0 {
			n = 1
		}
		return fmt.Sprintf("%d %s", n, i18n.Plural(n, "час", "часа", "часов"))
	default:
		n := int(d.Hours() / 24)
		return fmt.Sprintf("%d %s", n, i18n.Plural(n, "день", "дня", "дней"))
	}
}
