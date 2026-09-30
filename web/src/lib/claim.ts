// Состояния claim-флоу (спека §3.1) — чистые переходы, без React и сети.
//
// Флоу сознательно модель-центричный: ветвление по кодам ошибок сервера
// (409 no_chat_binding, 429 rate limit, 403 неверный код, 404 истёкший код)
// — единственное место, где UI может соврать. Поэтому переходы вынесены из
// компонента и покрыты табличными тестами: шит только рисует состояние и
// шлёт события.
import { errorCode, errorRetryAfterMs, errorStatus } from './groups';

/**
 * Состояние шита «Стать админом»:
 *  - no_binding — у группы нет привязанного чата: код некуда отправить;
 *  - idle — можно запрашивать код;
 *  - starting — запрос кода в полёте;
 *  - code_sent — код опубликован, ждём ввод;
 *  - confirming — проверка кода в полёте;
 *  - confirmed — роль admin выдана.
 */
export type ClaimState = 'no_binding' | 'idle' | 'starting' | 'code_sent' | 'confirming' | 'confirmed';

/** Данные успешного старта: срок жизни кода и целевой чат. */
export interface ClaimSession {
  /** RFC3339, UTC — когда код в чате перестанет приниматься. */
  expiresAt: string;
  chatId: number;
}

/** Полное состояние шита: статус + полезная нагрузка + причина отказа. */
export interface ClaimModel {
  state: ClaimState;
  session: ClaimSession | null;
  /** Текст ошибки под полем/кнопкой (уже локализованный вызывающим). */
  error: string | null;
  /** Задержка из Retry-After (мс) — для подписи «повторить через N». */
  retryAfterMs: number | null;
  /** Счётчик неудачных подтверждений: нужен, чтобы отличить «ещё раз» от серии. */
  attempts: number;
}

/** Стартовое состояние: до первого запроса шит в idle, но покажет подсказку
 * о привязке, если родитель уже знает, что чат не привязан (binding === null). */
export function initialClaimState(hasBinding: boolean): ClaimModel {
  return {
    state: hasBinding ? 'idle' : 'no_binding',
    session: null,
    error: null,
    retryAfterMs: null,
    attempts: 0,
  };
}

/** Нажата кнопка старта: уходим в «отправляем», прошлые ошибки чистим. */
export function onStartRequested(model: ClaimModel): ClaimModel {
  if (model.state === 'starting' || model.state === 'confirming') return model;
  return { ...model, state: 'starting', error: null, retryAfterMs: null };
}

/** Код опубликован в чат: переходим к вводу и храним срок его жизни. */
export function onStarted(model: ClaimModel, session: ClaimSession): ClaimModel {
  return {
    ...model,
    state: 'code_sent',
    session,
    error: null,
    retryAfterMs: null,
    attempts: 0,
  };
}

/** Нажато «подтвердить»: неверный формат кода ловит вызывающий (не сеть). */
export function onConfirmRequested(model: ClaimModel): ClaimModel {
  if (model.state !== 'code_sent') return model;
  return { ...model, state: 'confirming', error: null };
}

/** Роль выдана: единственное терминальное состояние, из него не выходят. */
export function onClaimConfirmed(model: ClaimModel): ClaimModel {
  return { ...model, state: 'confirmed', error: null, retryAfterMs: null };
}

/**
 * Разбор ошибки старта. Ветки спеки §3.1:
 *  - 409 `no_chat_binding` → no_binding: показываем инструкцию /bind_group;
 *  - 409 `claim_code_send_failed` → idle с текстом «проверьте, что бот админ»;
 *  - 429 → idle + Retry-After (не подсказка о привязке: чат есть);
 *  - 403 → idle (не участник / забанен).
 * Остальное — idle с текстом ошибки из конверта: молчание хуже.
 */
export function onStartFailed(
  model: ClaimModel,
  err: unknown,
  messages: ClaimMessages,
): ClaimModel {
  const code = errorCode(err);
  const retryAfterMs = errorRetryAfterMs(err);
  const base = { ...model, session: null, retryAfterMs, attempts: model.attempts };

  if (code === 'no_chat_binding') {
    return { ...base, state: 'no_binding', error: messages.noBinding };
  }
  if (code === 'rate_limit' || errorStatus(err) === 429) {
    return { ...base, state: 'idle', error: messages.rateLimited(retryAfterMs) };
  }
  if (code === 'claim_code_send_failed') {
    return { ...base, state: 'idle', error: messages.sendFailed };
  }
  return { ...base, state: 'idle', error: messages.generic(err) };
}

/**
 * Разбор ошибки подтверждения:
 *  - 403 `claim_bad_code` → остаёмся в code_sent: код в силе, пользователь
 *    просто опечатался (важно НЕ сбрасывать состояние — иначе пришлось бы
 *    запрашивать новый код из-за одной опечатки);
 *  - 404 / `claim_code_not_found` → idle: код истёк или погашен;
 *  - 429 (бюджет попыток) → code_sent с Retry-After: код ещё может быть жив;
 *  - 400 `validation` → code_sent: формат кода (до сети такое не доходит).
 */
export function onConfirmFailed(
  model: ClaimModel,
  err: unknown,
  messages: ClaimMessages,
): ClaimModel {
  const code = errorCode(err);
  const status = errorStatus(err);
  const retryAfterMs = errorRetryAfterMs(err);
  const attempts = model.attempts + 1;

  if (code === 'claim_bad_code' || (status === 403 && code !== 'no_chat_binding')) {
    return { ...model, state: 'code_sent', error: messages.badCode, retryAfterMs, attempts };
  }
  if (code === 'claim_code_not_found' || status === 404) {
    return {
      ...model,
      state: 'idle',
      session: null,
      error: messages.expired,
      retryAfterMs,
      attempts,
    };
  }
  if (status === 429) {
    return { ...model, state: 'code_sent', error: messages.rateLimited(retryAfterMs), retryAfterMs, attempts };
  }
  return { ...model, state: 'code_sent', error: messages.generic(err), retryAfterMs, attempts };
}

/** Активный код отозван (admin): возвращаемся к idle с пометкой об успехе. */
export function onRevoked(model: ClaimModel): ClaimModel {
  return { ...model, state: 'idle', session: null, error: null, retryAfterMs: null };
}

/** Текстовая политика флоу: локализация живёт в strings.ts, здесь — только
 * точки подстановки, чтобы чистые переходы не зависели от каталога. */
export interface ClaimMessages {
  noBinding: string;
  sendFailed: string;
  badCode: string;
  expired: string;
  /** Подпись 429: с Retry-After — «повторить через …», без него — общая. */
  rateLimited(retryAfterMs: number | null): string;
  /** Прочие ошибки: текст конверта API, иначе общий фолбэк. */
  generic(err: unknown): string;
}

/** Код claim'а — ровно 6 цифр (validClaimCode в claims_controller.go). */
export function isValidClaimCode(code: string): boolean {
  return /^[0-9]{6}$/.test(code);
}

/**
 * Оставшееся время жизни кода в миллисекундах (≤ 0 — истёк или битая дата).
 * Битый expires_at трактуем как истёкший: показать «ещё NaN» хуже, чем
 * предложить запросить новый код.
 */
export function claimRemainingMs(expiresAt: string, now: Date | number): number {
  const ms = new Date(expiresAt).getTime();
  if (Number.isNaN(ms)) return 0;
  const nowMs = now instanceof Date ? now.getTime() : now;
  return ms - nowMs;
}

/** Срок жизни кода-инвайта: «до 05.10.2026 12:00» либо «истёк». */
export function inviteExpired(expiresAt: string, now: Date | number): boolean {
  return claimRemainingMs(expiresAt, now) <= 0;
}