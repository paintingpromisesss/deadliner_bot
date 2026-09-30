// Перевод ошибок (клиентских и API) в тексты каталога. Одна точка правды:
// экраны не собирают формулировки сами, а компоненты не знают про коды ошибок.
import { errorCode, errorRetryAfterMs, errorStatus } from './groups';
import { strings, tpl } from './strings';
import { humanDuration } from './format';
import type { GroupFormError, SlugError } from './slug';

/** Текст ошибки клиентской валидации слага (зеркало domain/slug.go). */
export function slugErrorMessage(error: SlugError): string {
  switch (error) {
    case 'empty':
      return strings.groups.errSlugEmpty;
    case 'too_short':
      return strings.groups.errSlugTooShort;
    case 'too_long':
      return strings.groups.errSlugTooLong;
    case 'no_digit':
      return strings.groups.errSlugNoDigit;
    case 'charset':
    default:
      return strings.groups.errSlugCharset;
  }
}

/** Текст ошибки заголовка группы. */
export function groupTitleErrorMessage(error: GroupFormError): string {
  return error === 'title_long' ? strings.groups.errTitleLong : strings.groups.errTitleRequired;
}

/**
 * Текст ошибки лимита: с Retry-After — «повторить через 5 минут», без него —
 * общая формулировка. Точное значение сервер отдаёт заголовком (спека §3.3),
 * и показать «через сколько» полезнее, чем просто «слишком часто».
 */
export function rateLimitMessage(retryAfterMs: number | null): string {
  if (retryAfterMs === null || retryAfterMs <= 0) return strings.groups.errRateLimit;
  return tpl(strings.groups.errRateLimitRetry, humanDuration(retryAfterMs));
}

/**
 * Текст ошибки API для формы СОЗДАНИЯ группы и админских операций над ней:
 *  400 slug_invalid → ошибка слага;
 *  409 conflict → «номер занят» (создание группы — единственное место, где
 *     этот статус означает занятый слаг);
 *  429 → лимит с Retry-After;
 *  иначе — сообщение сервера, при его отсутствии — общий фолбэк.
 *
 * Для остальных путей 409 означает другое (см. redeemErrorMessage /
 * mutationErrorMessage): общий маппер здесь намеренно НЕ используется, иначе
 * отказ по лимиту использований читался бы как «номер занят».
 */
export function groupCreateErrorMessage(err: unknown): string {
  const code = errorCode(err);
  const status = errorStatus(err);
  if (status === 429) return rateLimitMessage(errorRetryAfterMs(err));
  if (code === 'slug_invalid') return strings.groups.errSlugCharset;
  if (code === 'conflict' || status === 409) return strings.groups.errSlugTaken;
  if (err instanceof Error && err.message) return err.message;
  return strings.common.actionFailed;
}

/**
 * Текст ошибки вступления по инвайт-коду. Здесь 409 — не «слаг занят», а
 * исчерпанный лимит использований: RedeemInvite атомарно тратит использование
 * (IncrementUsed → ErrConflict), и пользователю нужен новый код, а не совет
 * поискать группу по номеру.
 *
 * 404 (отозван/истёк/не существует — сервер намеренно не различает) и 400
 * (битый/пустой код) ведут к одной и той же мысли — «код не тот».
 */
export function redeemErrorMessage(err: unknown): string {
  const code = errorCode(err);
  const status = errorStatus(err);
  if (status === 429) return rateLimitMessage(errorRetryAfterMs(err));
  if (code === 'conflict' || status === 409) return strings.groups.errInviteExhausted;
  if (status === 404 || status === 400 || code === 'validation' || code === 'not_found') {
    return strings.groups.errCodeUnknown;
  }
  if (err instanceof Error && err.message) return err.message;
  return strings.common.actionFailed;
}

/**
 * Текст ошибки мутаций группы и инвайтов (отзыв кода, отзыв claim, выход,
 * удаление): реальные 409 здесь — «последний админ» и прочие конфликты
 * состояния, а не занятый слаг. Текст конверта API точнее любой подстановки,
 * поэтому приоритет у него; 429 по-прежнему несёт Retry-After.
 */
export function mutationErrorMessage(err: unknown): string {
  const code = errorCode(err);
  const status = errorStatus(err);
  if (status === 429) return rateLimitMessage(errorRetryAfterMs(err));
  if (code === 'last_admin') return strings.groups.errLastAdmin;
  if (err instanceof Error && err.message) return err.message;
  return strings.groups.errGroupGeneric;
}

/** Текст ошибки claim-флоу по коду/статусу ответа (спека §3.1). */
export function claimErrorMessage(err: unknown): string {
  const code = errorCode(err);
  const status = errorStatus(err);
  if (code === 'no_chat_binding') return strings.groups.errClaimNoBinding;
  if (code === 'claim_code_send_failed') return strings.groups.errClaimSendFailed;
  if (code === 'claim_bad_code') return strings.groups.errClaimBadCode;
  if (code === 'claim_code_not_found' || status === 404) return strings.groups.errClaimCodeExpired;
  if (status === 429) return rateLimitMessage(errorRetryAfterMs(err));
  if (code === 'validation' || status === 400) return strings.groups.errClaimFormat;
  if (code === 'last_admin') return strings.groups.errLastAdmin;
  if (err instanceof Error && err.message) return err.message;
  return strings.common.actionFailed;
}

/** Ошибка админских действий над участниками (в т.ч. 409 «последний админ»). */
export function memberActionErrorMessage(err: unknown): string {
  if (errorCode(err) === 'last_admin' || errorStatus(err) === 409) return strings.groups.errLastAdmin;
  if (err instanceof Error && err.message) return err.message;
  return strings.common.actionFailed;
}