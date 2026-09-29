// Тесты стора авторизации: bootstrap по сохранённому токену, вход по initData,
// patchMe, logout. fetch мокается — real API не нужен.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const getInitDataMock = vi.fn<() => string | undefined>();

vi.mock('../lib/tma', () => ({
  getInitData: () => getInitDataMock(),
}));

import { TOKEN_STORAGE_KEY, useAuthStore } from './auth';

const user = {
  id: 7,
  telegram_id: 777,
  username: 'durov',
  first_name: 'Pavel',
  tz: 'Europe/Moscow',
  dm_notify_default: true,
  is_superadmin: false,
};

const session = { token: 'session-token', user };

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface Call {
  url: string;
  method: string;
  headers: Headers;
  body: unknown;
}

let calls: Call[] = [];

function stubFetch(handler: (call: Call) => Response) {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers: new Headers(init?.headers),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    };
    calls.push(call);
    return handler(call);
  });
  vi.stubGlobal('fetch', fn);
}

function resetStore() {
  window.localStorage.clear();
  useAuthStore.setState({ token: null, user: null, status: 'init', error: null });
}

beforeEach(() => {
  calls = [];
  getInitDataMock.mockReset();
  resetStore();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('auth store: bootstrap', () => {
  it('с сохранённым токеном валидирует его через GET /me и персистит профиль', async () => {
    window.localStorage.setItem(TOKEN_STORAGE_KEY, 'stored-token');
    stubFetch(() => jsonResponse(200, user));

    await useAuthStore.getState().bootstrap();

    const state = useAuthStore.getState();
    expect(state.status).toBe('authed');
    expect(state.user).toEqual(user);
    expect(state.token).toBe('stored-token');
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('/api/v1/me');
    expect(calls[0].headers.get('Authorization')).toBe('Bearer stored-token');
  });

  it('протухший токен (401) отбрасывается и выполняется вход по initData', async () => {
    window.localStorage.setItem(TOKEN_STORAGE_KEY, 'stale');
    getInitDataMock.mockReturnValue('query_id=1&hash=abc');
    stubFetch((call) => {
      if (call.url === '/api/v1/auth/telegram') return jsonResponse(200, session);
      return jsonResponse(401, { error: { code: 'unauthorized', message: 'Требуется авторизация' } });
    });

    await useAuthStore.getState().bootstrap();

    const state = useAuthStore.getState();
    expect(state.status).toBe('authed');
    expect(state.token).toBe('session-token');
    expect(window.localStorage.getItem(TOKEN_STORAGE_KEY)).toBe('session-token');
    // Первый /me + сам логин; повторного /me нет — логин уже вернул профиль.
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/me', '/api/v1/auth/telegram']);
    expect(calls[1].body).toEqual({ initData: 'query_id=1&hash=abc' });
  });

  it('без токена и без initData (обычный браузер) → status=error с понятным текстом', async () => {
    getInitDataMock.mockReturnValue(undefined);
    stubFetch(() => jsonResponse(200, user));

    await useAuthStore.getState().bootstrap();

    const state = useAuthStore.getState();
    expect(state.status).toBe('error');
    expect(state.error).toContain('Telegram');
    expect(calls).toHaveLength(0);
  });

  it('ошибка сервера на /me не превращается в initData-вход', async () => {
    window.localStorage.setItem(TOKEN_STORAGE_KEY, 'stored');
    getInitDataMock.mockReturnValue('query_id=1&hash=abc');
    stubFetch(() => jsonResponse(500, { error: { code: 'internal', message: 'Внутренняя ошибка' } }));

    await useAuthStore.getState().bootstrap();

    expect(useAuthStore.getState().status).toBe('error');
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/me']);
  });
});

describe('auth store: login / patchMe / logout', () => {
  it('login по initData сохраняет токен и профиль', async () => {
    getInitDataMock.mockReturnValue('query_id=2&hash=def');
    stubFetch(() => jsonResponse(200, session));

    await useAuthStore.getState().login();

    expect(useAuthStore.getState().status).toBe('authed');
    expect(useAuthStore.getState().user).toEqual(user);
    expect(window.localStorage.getItem(TOKEN_STORAGE_KEY)).toBe('session-token');
    expect(calls[0].url).toBe('/api/v1/auth/telegram');
    expect(calls[0].headers.get('Authorization')).toBeNull();
  });

  it('login без initData не делает запрос', async () => {
    getInitDataMock.mockReturnValue(undefined);
    stubFetch(() => jsonResponse(200, session));

    await expect(useAuthStore.getState().login()).rejects.toMatchObject({ code: 'no_init_data' });
    expect(calls).toHaveLength(0);
  });

  it('patchMe делает PATCH /me и обновляет профиль в сторе', async () => {
    useAuthStore.setState({ token: 't', user, status: 'authed' });
    const updated = { ...user, tz: 'Asia/Yekaterinburg', dm_notify_default: false };
    stubFetch(() => jsonResponse(200, updated));

    await useAuthStore.getState().patchMe({ tz: 'Asia/Yekaterinburg', dm_notify_default: false });

    expect(calls[0].method).toBe('PATCH');
    expect(calls[0].url).toBe('/api/v1/me');
    expect(calls[0].body).toEqual({ tz: 'Asia/Yekaterinburg', dm_notify_default: false });
    expect(useAuthStore.getState().user).toEqual(updated);
  });

  it('logout отзывает сессию, чистит localStorage и стор', async () => {
    useAuthStore.setState({ token: 't', user, status: 'authed' });
    window.localStorage.setItem(TOKEN_STORAGE_KEY, 't');
    stubFetch(() => new Response(null, { status: 204 }));

    await useAuthStore.getState().logout();

    expect(calls[0].url).toBe('/api/v1/me/logout');
    expect(calls[0].method).toBe('POST');
    expect(window.localStorage.getItem(TOKEN_STORAGE_KEY)).toBeNull();
    expect(useAuthStore.getState().token).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
    expect(useAuthStore.getState().status).toBe('error');
  });

  it('logout не отправляет повторный вход при 401', async () => {
    useAuthStore.setState({ token: 't', user, status: 'authed' });
    stubFetch(() => jsonResponse(401, { error: { code: 'unauthorized', message: 'Требуется авторизация' } }));

    await useAuthStore.getState().logout();

    expect(calls.map((c) => c.url)).toEqual(['/api/v1/me/logout']);
    expect(useAuthStore.getState().token).toBeNull();
  });
});