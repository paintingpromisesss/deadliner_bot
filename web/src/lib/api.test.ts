import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, configureAuth, type Session } from './api';

const user = {
  id: 1,
  telegram_id: 42,
  username: 'durov',
  first_name: 'Pavel',
  tz: 'Europe/Moscow',
  dm_notify_default: true,
  is_superadmin: false,
};

const session: Session = { token: 'fresh-token', user };

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function unauthorized(): Response {
  return jsonResponse(401, { error: { code: 'unauthorized', message: 'Требуется авторизация' } });
}

interface Calls {
  auth: number;
  urls: string[];
  authHeaders: (string | null)[];
}

/** Мок fetch: сценарий задаётся очередью обработчиков. */
function mockFetch(handler: (url: string, init: RequestInit, calls: Calls) => Response) {
  const calls: Calls = { auth: 0, urls: [], authHeaders: [] };
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const req = init ?? {};
    calls.urls.push(url);
    const headers = new Headers(req.headers);
    calls.authHeaders.push(headers.get('Authorization'));
    if (url === '/api/v1/auth/telegram') calls.auth += 1;
    return handler(url, req, calls);
  });
  vi.stubGlobal('fetch', fn);
  return { calls, fn };
}

beforeEach(() => {
  configureAuth({
    getToken: () => 'stale-token',
    getInitData: () => 'query_id=1&hash=deadbeef',
    onSession: () => {},
    onAuthFailure: () => {},
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('apiFetch', () => {
  it('подставляет Bearer-токен в Authorization', async () => {
    const { calls } = mockFetch(() => jsonResponse(200, user));

    const me = await api.fetch<typeof user>('/me');

    expect(me).toEqual(user);
    expect(calls.authHeaders).toEqual(['Bearer stale-token']);
    expect(calls.auth).toBe(0);
  });

  it('на 401 переавторизуется по initData ровно один раз и повторяет запрос', async () => {
    let meCalls = 0;
    const { calls } = mockFetch((url, _init, c) => {
      if (url === '/api/v1/auth/telegram') return jsonResponse(200, session);
      meCalls += 1;
      // Первый /me — со старым токеном (401), второй — с новым.
      return c.authHeaders.at(-1) === 'Bearer fresh-token' ? jsonResponse(200, user) : unauthorized();
    });

    const me = await api.fetch<typeof user>('/me');

    expect(me).toEqual(user);
    expect(meCalls).toBe(2);
    expect(calls.auth).toBe(1);
    expect(calls.authHeaders).toEqual(['Bearer stale-token', null, 'Bearer fresh-token']);
  });

  it('не зацикливается: второй 401 после переавторизации уходит в onAuthFailure', async () => {
    const onAuthFailure = vi.fn();
    configureAuth({ onAuthFailure });
    let meCalls = 0;
    const { calls } = mockFetch((url) => {
      if (url === '/api/v1/auth/telegram') return jsonResponse(200, session);
      meCalls += 1;
      return unauthorized();
    });

    await expect(api.fetch('/me')).rejects.toBeInstanceOf(ApiError);

    expect(meCalls).toBe(2); // исходный + ровно один повтор
    expect(calls.auth).toBe(1);
    expect(onAuthFailure).toHaveBeenCalledTimes(1);
    expect(onAuthFailure.mock.calls[0][0]).toBe('Требуется авторизация');
  });

  it('без initData повторной авторизации нет — сразу onAuthFailure', async () => {
    const onAuthFailure = vi.fn();
    configureAuth({ getInitData: () => null, onAuthFailure });
    const { calls } = mockFetch(() => unauthorized());

    await expect(api.fetch('/me')).rejects.toMatchObject({ status: 401, code: 'unauthorized' });

    expect(calls.auth).toBe(0);
    expect(calls.urls).toEqual(['/api/v1/me']);
    expect(onAuthFailure).toHaveBeenCalledTimes(1);
  });

  it('параллельные 401 делят один повторный логин', async () => {
    const { calls } = mockFetch((url, _init, c) => {
      if (url === '/api/v1/auth/telegram') return jsonResponse(200, session);
      return c.authHeaders.at(-1) === 'Bearer fresh-token' ? jsonResponse(200, user) : unauthorized();
    });

    await Promise.all([api.fetch('/me'), api.fetch('/me'), api.fetch('/me')]);

    expect(calls.auth).toBe(1);
  });

  it('noReauth отключает повторный вход (logout)', async () => {
    const { calls } = mockFetch(() => unauthorized());

    await expect(api.fetch('/me/logout', { method: 'POST', noReauth: true })).rejects.toBeInstanceOf(
      ApiError,
    );

    expect(calls.auth).toBe(0);
    expect(calls.urls).toEqual(['/api/v1/me/logout']);
  });

  it('парсит конверт ошибки в ApiError{status, code, message}', async () => {
    mockFetch(() =>
      jsonResponse(409, { error: { code: 'conflict', message: 'Конфликт: операция недопустима' } }),
    );

    await expect(api.fetch('/groups/1', { method: 'DELETE' })).rejects.toEqual(
      expect.objectContaining({
        status: 409,
        code: 'conflict',
        message: 'Конфликт: операция недопустима',
      }),
    );
  });

  it('провал повторного входа отдаёт причину логина в onAuthFailure, а бросает исходный 401', async () => {
    const onAuthFailure = vi.fn();
    configureAuth({ onAuthFailure });
    const { calls } = mockFetch((url) => {
      if (url === '/api/v1/auth/telegram') {
        // Причина отказа входа отличается от сообщения 401 на /me.
        return jsonResponse(401, {
          error: { code: 'auth_failed', message: 'initData истёк — откройте Mini App заново' },
        });
      }
      return unauthorized();
    });

    // Бросается исходный 401 (/me), а не ошибка логина: статус и код
    // принадлежат тому запросу, который упал.
    await expect(api.fetch('/me')).rejects.toMatchObject({ status: 401, code: 'unauthorized' });

    expect(calls.auth).toBe(1);
    // Причина отказа входа видна пользователю, а не проглочена.
    expect(onAuthFailure).toHaveBeenCalledTimes(1);
    expect(onAuthFailure.mock.calls[0][0]).toBe('initData истёк — откройте Mini App заново');
  });

  it('сетевой сбой повторного входа не превращается в «нет initData»', async () => {
    const onAuthFailure = vi.fn();
    configureAuth({ onAuthFailure });
    mockFetch((url) => {
      if (url === '/api/v1/auth/telegram') throw new Error('Failed to fetch');
      return unauthorized();
    });

    await expect(api.fetch('/me')).rejects.toMatchObject({ status: 401 });

    expect(onAuthFailure).toHaveBeenCalledTimes(1);
    expect(onAuthFailure.mock.calls[0][0]).toContain('Failed to fetch');
  });

  it('204 возвращает undefined и не пытается парсить тело', async () => {
    mockFetch(() => new Response(null, { status: 204 }));

    await expect(api.fetch('/me/logout', { method: 'POST' })).resolves.toBeUndefined();
  });

  it('auth:false не отправляет Authorization (сам логин)', async () => {
    const { calls } = mockFetch(() => jsonResponse(200, session));

    await api.fetch('/auth/telegram', { method: 'POST', body: { initData: 'x' }, auth: false });

    expect(calls.authHeaders).toEqual([null]);
  });
});