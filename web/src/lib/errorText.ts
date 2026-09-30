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
 * Текст ошибки API для форм групп и инвайтов:
 *  409 conflict → «номер занят» (создание) либо текст конверта;
 *  400 slug_invalid → ошибка слага;
 *  429 → лимит с Retry-After;
 *  404 → неизвестный/отозванный/истёкший код;
 *  иначе — сообщение сервера, при его отсутствии — общий фолбэк.
 */
export function groupApiErrorMessage(err: unknown): string {
  const code = errorCode(err);
  const status = errorStatus(err);
  if (status === 429) return rateLimitMessage(errorRetryAfterMs(err));
  if (code === 'slug_invalid') return strings.groups.errSlugCharset;
  if (code === 'conflict' || status === 409) return strings.groups.errSlugTaken;
  if (status === 404) return strings.groups.errCodeUnknown;
  if (err instanceof Error && err.message) return err.message;
  return strings.common.actionFailed;
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