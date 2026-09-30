// HTTP-клиент REST API (спека §5): Bearer-токен из стора авторизации,
// разбор конверта ошибок {"error":{code,message}}, одна повторная
// авторизация по initData при 401.
//
// Зависимость от стора — через инъекцию хуков (configureAuth), а не импортом
// стора: так `api.ts` не образует цикла импортов со стором и тестируется сам
// по себе.
import { getInitData } from './tma';

const API_BASE = '/api/v1';

/** Профиль пользователя — DTO GET /api/v1/me. */
export interface User {
  id: number;
  telegram_id: number;
  username: string;
  first_name: string;
  tz: string;
  dm_notify_default: boolean;
  is_superadmin: boolean;
}

/** Ответ POST /api/v1/auth/telegram. */
export interface Session {
  token: string;
  user: User;
}

/** Ошибка API: HTTP-статус + код и сообщение из конверта. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /**
   * Задержка из заголовка Retry-After (мс), если сервер её прислал: 429 на
   * создании группы и на claim-флоу несёт её, и интерфейс обязан показать
   * «через сколько», а не просто «слишком часто».
   */
  retryAfterMs: number | null = null;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

/**
 * Дополняет ошибку ответа задержкой из Retry-After (спека §3.3: лимиты
 * отдают 429 с этим заголовком). Значение в секундах, как требует RFC.
 */
export function withRetryAfter(err: ApiError, res: Response): ApiError {
  if (err.status !== 429) return err;
  const raw = res.headers.get('Retry-After');
  if (!raw) return err;
  const seconds = Number(raw);
  if (!Number.isFinite(seconds) || seconds < 0) return err;
  err.retryAfterMs = Math.round(seconds * 1000);
  return err;
}

/** Хуки авторизации, которые устанавливает стор (stores/auth.ts). */
export interface AuthHooks {
  /** Текущий Bearer-токен (null — не авторизованы). */
  getToken(): string | null;
  /** Свежий raw initData для повторного входа (null — недоступен). */
  getInitData(): string | null;
  /** Сохранить выданную сессию. */
  onSession(session: Session): void;
  /** Авторизация невозможна — приложение должно показать экран ошибки. */
  onAuthFailure(message: string): void;
  /**
   * Разрешён ли автоматический повторный вход. false после осознанного
   * выхода: иначе фоновый запрос, получивший 401, молча залогинил бы
   * пользователя обратно по initData и нейтральный экран выхода исчез бы.
   */
  canReauth(): boolean;
}

let hooks: AuthHooks = {
  getToken: () => null,
  getInitData: () => getInitData() ?? null,
  onSession: () => {},
  onAuthFailure: () => {},
  canReauth: () => true,
};

/** Устанавливает (частично) хуки авторизации. Вызывается один раз при старте. */
export function configureAuth(next: Partial<AuthHooks>): void {
  hooks = { ...hooks, ...next };
}

/** Опции запроса. */
export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE';
  body?: unknown;
  /** false — запрос без Authorization (например, сам логин). */
  auth?: boolean;
  /** true — не пытаться повторно авторизоваться при 401 (logout). */
  noReauth?: boolean;
  signal?: AbortSignal;
}

async function rawRequest(path: string, opts: RequestOptions, token: string | null): Promise<Response> {
  const headers = new Headers();
  if (opts.auth !== false && token) headers.set('Authorization', `Bearer ${token}`);
  let body: string | undefined;
  if (opts.body !== undefined) {
    headers.set('Content-Type', 'application/json');
    body = JSON.stringify(opts.body);
  }
  return fetch(`${API_BASE}${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body,
    signal: opts.signal,
  });
}

async function readError(res: Response): Promise<ApiError> {
  let code = 'http_error';
  let message = `HTTP ${res.status}`;
  try {
    const data = (await res.json()) as { error?: { code?: string; message?: string } };
    if (data?.error?.code) code = data.error.code;
    if (data?.error?.message) message = data.error.message;
  } catch {
    // Тело не JSON — оставляем сообщение по статусу.
  }
  return withRetryAfter(new ApiError(res.status, code, message), res);
}

/** Дедупликация параллельных повторных логинов: один запрос на пачку 401. */
let reauthInFlight: Promise<ReauthResult> | null = null;

/** Результат повторного входа: сессия либо причина отказа. */
type ReauthResult = { session: Session; error: null } | { session: null; error: Error };

/**
 * Повторный вход по initData. Ошибку логина НЕ проглатываем: она уходит в
 * onAuthFailure с текстом причины (сервер недоступен, протух initData), а
 * вызывающий затем бросает исходный 401 — пользователь видит понятную
 * причину, а не безликое «Требуется авторизация».
 *
 * Промис нормализован (никогда не отклоняется), поэтому параллельные
 * вызывающие получают один и тот же результат, а не разный.
 */
async function reauthenticate(): Promise<ReauthResult> {
  if (reauthInFlight) return reauthInFlight;

  // После осознанного выхода повторный вход только по явному действию
  // пользователя (кнопка «Войти снова» → bootstrap), не по фоновому 401.
  if (!hooks.canReauth()) {
    return {
      session: null,
      error: new ApiError(0, 'logged_out', 'Нужен повторный вход.'),
    };
  }

  const initData = hooks.getInitData();
  if (!initData) {
    return {
      session: null,
      error: new ApiError(0, 'no_init_data', 'Нет initData для повторного входа.'),
    };
  }

  const attempt: Promise<ReauthResult> = authenticate(initData)
    .then(
      (session): ReauthResult => ({ session, error: null }),
      (e): ReauthResult => ({ session: null, error: e instanceof Error ? e : new Error(String(e)) }),
    )
    .finally(() => {
      reauthInFlight = null;
    });

  reauthInFlight = attempt;
  return attempt;
}

/**
 * Вход по initData: POST /api/v1/auth/telegram. Выданная сессия сразу
 * сохраняется через хук onSession.
 */
export async function authenticate(initData: string): Promise<Session> {
  const res = await fetch(`${API_BASE}/auth/telegram`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ initData }),
  });
  if (!res.ok) throw await readError(res);
  const session = (await res.json()) as Session;
  hooks.onSession(session);
  return session;
}

/**
 * Запрос к API. При 401 — ровно одна повторная авторизация по initData
 * и один повтор; повторный 401 переводит приложение в состояние ошибки.
 */
export async function apiFetch<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const withAuth = opts.auth !== false;
  let res = await rawRequest(path, opts, withAuth ? hooks.getToken() : null);

  if (res.status === 401 && withAuth && !opts.noReauth) {
    const { session, error } = await reauthenticate();
    if (!session) {
      // Причина отказа входа информативнее исходного 401 — показываем её,
      // но бросаем исходную ошибку: код и статус ответа принадлежат ей.
      const err = await readError(res);
      hooks.onAuthFailure(error?.message ?? err.message);
      throw err;
    }
    res = await rawRequest(path, opts, session.token);
    if (res.status === 401) {
      const err = await readError(res);
      hooks.onAuthFailure(err.message);
      throw err;
    }
  }

  if (!res.ok) throw await readError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** Пространство имён для читающих вызовов: `api.fetch<T>('/me')`. */
export const api = { fetch: apiFetch };