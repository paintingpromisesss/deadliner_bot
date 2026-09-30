// Логика claim-флоу и клиентского зеркала слага — табличные тесты без DOM.
//
// Почему именно эти два блока: claim — единственное место, где интерфейс может
// соврать пользователю о состоянии (прислал ли бот код, жив ли он, верен ли
// ввод), а слаг — единственная валидация, которую клиент дублирует у сервера
// (каждая неудачная попытка тратит серверный лимит «3 группы в сутки»).
// Обе функции чистые, поэтому проверяются исчерпывающе, без моков сети.
import { describe, expect, it } from 'vitest';

import {
  claimRemainingMs,
  initialClaimState,
  isValidClaimCode,
  onClaimConfirmed,
  onConfirmFailed,
  onConfirmRequested,
  onRevoked,
  onStartFailed,
  onStartRequested,
  onStarted,
  type ClaimMessages,
} from '../../lib/claim';
import { ApiError } from '../../lib/api';
import { checkSlug, normalizeSlug, validateGroupTitle, validateSlugStrict } from '../../lib/slug';
import { routeGroupID, parseHash } from '../../router';

/** Политика текстов для тестов: подстановка видна, поэтому проверяем и её. */
const messages: ClaimMessages = {
  noBinding: 'нет привязки',
  sendFailed: 'не отправилось',
  badCode: 'неверный код',
  expired: 'код истёк',
  rateLimited: (ms) => (ms === null ? 'слишком часто' : `слишком часто: ${ms}`),
  generic: (err) => (err instanceof Error ? err.message : 'ошибка'),
};

/** Ошибка API с заголовком Retry-After (как её отдаёт apiFetch). */
function apiError(status: number, code: string, retryAfterMs: number | null = null): ApiError {
  const err = new ApiError(status, code, `HTTP ${status}`);
  err.retryAfterMs = retryAfterMs;
  return err;
}

describe('claim: стартовое состояние', () => {
  it('с привязкой чата — idle, без привязки — no_binding', () => {
    expect(initialClaimState(true).state).toBe('idle');
    expect(initialClaimState(false).state).toBe('no_binding');
  });

  it('в no_binding не остаётся ни сессии, ни ошибки', () => {
    const model = initialClaimState(false);
    expect(model.session).toBeNull();
    expect(model.error).toBeNull();
    expect(model.retryAfterMs).toBeNull();
  });
});

describe('claim: успешный путь idle → code_sent → confirmed', () => {
  it('проходит шаги start → started → confirm → confirmed', () => {
    let model = initialClaimState(true);
    expect(model.state).toBe('idle');

    model = onStartRequested(model);
    expect(model.state).toBe('starting');

    model = onStarted(model, { expiresAt: '2026-10-01T12:10:00Z', chatId: -100123 });
    expect(model.state).toBe('code_sent');
    expect(model.session?.chatId).toBe(-100123);
    // Ошибки прошлого шага не переносятся в новый.
    expect(model.error).toBeNull();

    model = onConfirmRequested(model);
    expect(model.state).toBe('confirming');

    model = onClaimConfirmed(model);
    expect(model.state).toBe('confirmed');
    expect(model.error).toBeNull();
  });

  it('повторный start во время отправки/подтверждения игнорируется (нет двойной отправки)', () => {
    const starting = onStartRequested(initialClaimState(true));
    expect(onStartRequested(starting)).toBe(starting);

    const confirming = onConfirmRequested(
      onStarted(initialClaimState(true), { expiresAt: '2026-10-01T12:10:00Z', chatId: 1 }),
    );
    expect(onStartRequested(confirming)).toBe(confirming);
  });

  it('подтверждение невозможно вне code_sent', () => {
    const idle = initialClaimState(true);
    expect(onConfirmRequested(idle)).toBe(idle);
  });
});

describe('claim: ошибки старта', () => {
  it('409 no_chat_binding переводит в no_binding с подсказкой о привязке', () => {
    const model = onStartFailed(
      onStartRequested(initialClaimState(true)),
      apiError(409, 'no_chat_binding'),
      messages,
    );
    expect(model.state).toBe('no_binding');
    expect(model.error).toBe('нет привязки');
  });

  it('429 несёт Retry-After и остаётся в idle (чат есть, дело не в привязке)', () => {
    const model = onStartFailed(
      onStartRequested(initialClaimState(true)),
      apiError(429, 'rate_limit', 60_000),
      messages,
    );
    expect(model.state).toBe('idle');
    expect(model.retryAfterMs).toBe(60_000);
    expect(model.error).toBe('слишком часто: 60000');
  });

  it('429 без Retry-After даёт общую формулировку', () => {
    const model = onStartFailed(
      onStartRequested(initialClaimState(true)),
      apiError(429, 'rate_limit'),
      messages,
    );
    expect(model.state).toBe('idle');
    expect(model.error).toBe('слишком часто');
  });

  it('409 claim_code_send_failed объясняет, что бот не смог написать в чат', () => {
    const model = onStartFailed(
      onStartRequested(initialClaimState(true)),
      apiError(409, 'claim_code_send_failed'),
      messages,
    );
    expect(model.state).toBe('idle');
    expect(model.error).toBe('не отправилось');
  });

  it('403 (не участник) не выдаёт себя за отсутствие привязки', () => {
    const model = onStartFailed(
      onStartRequested(initialClaimState(true)),
      apiError(403, 'forbidden'),
      messages,
    );
    expect(model.state).toBe('idle');
    expect(model.error).toBe('HTTP 403');
  });

  it('сетевая ошибка тоже видна пользователю', () => {
    const model = onStartFailed(
      onStartRequested(initialClaimState(true)),
      new Error('Failed to fetch'),
      messages,
    );
    expect(model.state).toBe('idle');
    expect(model.error).toBe('Failed to fetch');
  });

  it('провал старта не оставляет ссылку на прошлую сессию кода', () => {
    const withSession = onStarted(initialClaimState(true), {
      expiresAt: '2026-10-01T12:10:00Z',
      chatId: 5,
    });
    const failed = onStartFailed(onStartRequested(withSession), apiError(429, 'rate_limit'), messages);
    expect(failed.session).toBeNull();
  });
});

describe('claim: ошибки подтверждения', () => {
  const sent = () =>
    onStarted(initialClaimState(true), { expiresAt: '2026-10-01T12:10:00Z', chatId: 7 });

  it('403 claim_bad_code остаётся в code_sent: код ещё можно ввести верно', () => {
    const model = onConfirmFailed(onConfirmRequested(sent()), apiError(403, 'claim_bad_code'), messages);
    expect(model.state).toBe('code_sent');
    expect(model.error).toBe('неверный код');
    // Сессия кода сохраняется — иначе пользователю пришлось бы запрашивать новый.
    expect(model.session).not.toBeNull();
  });

  it('серия неверных кодов всё так же оставляет ввод активным', () => {
    let model = sent();
    model = onConfirmFailed(model, apiError(403, 'claim_bad_code'), messages);
    model = onConfirmFailed(model, apiError(403, 'claim_bad_code'), messages);
    expect(model.state).toBe('code_sent');
    expect(model.session).not.toBeNull();
  });

  it('404 claim_code_not_found возвращает в idle с текстом об истечении', () => {
    const model = onConfirmFailed(
      onConfirmRequested(sent()),
      apiError(404, 'claim_code_not_found'),
      messages,
    );
    expect(model.state).toBe('idle');
    expect(model.session).toBeNull();
    expect(model.error).toBe('код истёк');
  });

  it('404 без кода (истёк на стороне сервера) трактуется так же', () => {
    const model = onConfirmFailed(onConfirmRequested(sent()), apiError(404, 'not_found'), messages);
    expect(model.state).toBe('idle');
    expect(model.error).toBe('код истёк');
  });

  it('429 (бюджет попыток) остаётся в code_sent и несёт Retry-After', () => {
    const model = onConfirmFailed(
      onConfirmRequested(sent()),
      apiError(429, 'rate_limit', 120_000),
      messages,
    );
    expect(model.state).toBe('code_sent');
    expect(model.retryAfterMs).toBe(120_000);
    expect(model.error).toBe('слишком часто: 120000');
  });

  it('400 (битый формат, дошёл до сервера) не выкидывает из ввода кода', () => {
    const model = onConfirmFailed(
      onConfirmRequested(sent()),
      apiError(400, 'validation'),
      messages,
    );
    expect(model.state).toBe('code_sent');
  });

  it('отзыв кода админом возвращает флоу к idle', () => {
    const model = onRevoked(sent());
    expect(model.state).toBe('idle');
    expect(model.session).toBeNull();
    expect(model.error).toBeNull();
  });
});

describe('claim: формат кода', () => {
  it('принимает ровно 6 цифр, включая ведущие нули', () => {
    expect(isValidClaimCode('000000')).toBe(true);
    expect(isValidClaimCode('123456')).toBe(true);
  });

  it('отвергает нецифры, другую длину и пустую строку', () => {
    expect(isValidClaimCode('12345')).toBe(false);
    expect(isValidClaimCode('1234567')).toBe(false);
    expect(isValidClaimCode('12345a')).toBe(false);
    expect(isValidClaimCode('')).toBe(false);
    expect(isValidClaimCode(' 123456')).toBe(false);
  });
});

describe('claim: остаток времени кода', () => {
  it('считает разницу до срока', () => {
    const now = new Date('2026-10-01T12:00:00Z');
    expect(claimRemainingMs('2026-10-01T12:10:00Z', now)).toBe(600_000);
  });

  it('истёкший код даёт неположительный остаток', () => {
    const now = new Date('2026-10-01T12:00:00Z');
    expect(claimRemainingMs('2026-10-01T11:59:59Z', now)).toBeLessThanOrEqual(0);
  });

  it('битая дата трактуется как «истёк», а не как NaN-срок', () => {
    expect(claimRemainingMs('не дата', new Date('2026-10-01T12:00:00Z'))).toBe(0);
  });
});

// --- Клиентское зеркало слага (domain/slug.go) ------------------------------

describe('normalizeSlug: как domain.Normalize', () => {
  it('приводит к верхнему регистру, убирает пробелы и ё → Ё', () => {
    expect(normalizeSlug('  икбо-33-21 ')).toBe('ИКБО-33-21');
    expect(normalizeSlug('м8о 401б 23')).toBe('М8О401Б23');
    expect(normalizeSlug('ёж-1')).toBe('ЁЖ-1');
  });
});

describe('validateSlugStrict: таблица приёма/отказа', () => {
  const accepted = [
    'М8О-401Б-23',
    'ИКБО-33-21',
    'A1B',
    'АБВ12',
    'М8О401Б23',
    '123',
    'ABC-1-DEF-2',
    'А' + '1'.repeat(15), // ровно 16 рун
  ];

  it.each(accepted)('принимает %s', (slug) => {
    expect(validateSlugStrict(slug)).toBeNull();
  });

  const rejected: [string, string][] = [
    ['', 'empty'],
    ['А1', 'too_short'],
    ['М8', 'too_short'],
    ['A1', 'too_short'],
    ['А' + '1'.repeat(16), 'too_long'],
    ['М8О--401Б-23', 'charset'],
    ['-М8О', 'charset'],
    ['М8О-', 'charset'],
    ['М8О_401', 'charset'],
    ['икбо-33-21', 'charset'],
    ['М8О-401Б-23!', 'charset'],
    ['ЁЖ-1', 'charset'], // ё переводится в Ё, но Ё вне диапазона А..Я
    ['АБВГД', 'no_digit'],
    ['ABC', 'no_digit'],
  ];

  it.each(rejected)('отвергает %s как %s', (slug, reason) => {
    expect(validateSlugStrict(slug)).toBe(reason);
  });

  it('подсказки вузовских форматов проходят целиком (нормализация + проверка)', () => {
    expect(checkSlug('м8о-401б-23')).toEqual({ normalized: 'М8О-401Б-23', error: null });
    expect(checkSlug(' икбо-33-21 ')).toEqual({ normalized: 'ИКБО-33-21', error: null });
    // Сервер удаляет ВСЕ пробелы (domain.Normalize), поэтому «М8О 401Б» — это
    // валидный «М8О401Б», а не ошибка; зеркало обязано вести себя так же.
    expect(checkSlug('М8О 401Б')).toEqual({ normalized: 'М8О401Б', error: null });
  });

  it('пустая строка — отдельная причина, а не «слишком коротко»', () => {
    expect(checkSlug('   ').error).toBe('empty');
  });
});

describe('validateGroupTitle', () => {
  it('требует непустой заголовок и ограничивает длину 200 символами', () => {
    expect(validateGroupTitle('  ')).toBe('title_required');
    expect(validateGroupTitle('М8О-401Б-23')).toBeNull();
    expect(validateGroupTitle('a'.repeat(200))).toBeNull();
    expect(validateGroupTitle('a'.repeat(201))).toBe('title_long');
  });
});

// --- Разбор маршрута группы -------------------------------------------------

describe('routeGroupID', () => {
  it('достаёт числовой id из #/groups/123', () => {
    expect(routeGroupID(parseHash('#/groups/123'))).toBe(123);
  });

  it('без id (список групп) возвращает null', () => {
    expect(routeGroupID(parseHash('#/groups'))).toBeNull();
    expect(routeGroupID(parseHash('#/calendar'))).toBeNull();
  });

  it('мусор в id не роняет разбор маршрута', () => {
    expect(routeGroupID(parseHash('#/groups/abc'))).toBeNull();
    expect(routeGroupID(parseHash('#/groups/0'))).toBeNull();
    expect(routeGroupID(parseHash('#/groups/-5'))).toBeNull();
    expect(routeGroupID(parseHash('#/groups/1e3'))).toBeNull();
    expect(routeGroupID(parseHash('#/groups/99999999999999999999'))).toBeNull();
  });
});