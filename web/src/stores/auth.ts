// Состояние авторизации (zustand): токен сессии, профиль, статус bootstrap.
//
// Токен персистится в localStorage (ключ 'dl_token'), чтобы перезапуск Mini App
// не требовал полного цикла входа. Bootstrap:
//   сохранённый токен → GET /me → authed;
//   иначе → POST /auth/telegram с initData → authed;
//   нет ни того, ни другого (обычный браузер без Telegram) → error.
import { create } from 'zustand';
import { ApiError, apiFetch, api, authenticate, configureAuth, type Session, type User } from '../lib/api';
import { getInitData } from '../lib/tma';

export const TOKEN_STORAGE_KEY = 'dl_token';

export type AuthStatus = 'init' | 'authed' | 'anonymous' | 'error';

interface AuthState {
  token: string | null;
  user: User | null;
  status: AuthStatus;
  /** Текст ошибки для экрана сбоя авторизации. */
  error: string | null;
  /** Bootstrap: восстановить сессию или войти по initData. */
  bootstrap(): Promise<void>;
  /** Вход по initData (используется и при повторной авторизации). */
  login(initData?: string): Promise<void>;
  /** Выход: отзыв сессии на сервере + очистка локального состояния. */
  logout(): Promise<void>;
  /** Локальная установка сессии (вызывается api-клиентом после re-auth). */
  setSession(session: Session): void;
  /** PATCH /me: частичное обновление профиля. */
  patchMe(patch: { tz?: string; dm_notify_default?: boolean }): Promise<void>;
  /** Готовность к запросам к API. */
  isAuthed(): boolean;
}

function readStoredToken(): string | null {
  try {
    return window.localStorage.getItem(TOKEN_STORAGE_KEY);
  } catch {
    // Приватный режим/запрет storage — работаем без персиста.
    return null;
  }
}

function writeStoredToken(token: string | null): void {
  try {
    if (token === null) window.localStorage.removeItem(TOKEN_STORAGE_KEY);
    else window.localStorage.setItem(TOKEN_STORAGE_KEY, token);
  } catch {
    // Игнорируем: сессия проживёт до перезагрузки страницы.
  }
}

/**
 * Single-flight бутстрапа: StrictMode монтирует эффект дважды, и без общего
 * промиса вышло бы два GET /me и два входа по initData. Тот же приём, что в
 * api.ts для повторной авторизации.
 */
let bootstrapInFlight: Promise<void> | null = null;

type SetState = (partial: Partial<AuthState>) => void;

/**
 * Восстановление сессии: сохранённый токен → GET /me; иначе вход по initData.
 * Вынесено из стора, чтобы single-flight-обёртка в bootstrap() оставалась
 * читаемой.
 */
async function runBootstrap(set: SetState, get: () => AuthState): Promise<void> {
  const stored = readStoredToken();
  if (stored) {
    set({ token: stored, status: 'init', error: null });
    try {
      // noReauth: протухший токен здесь не повод для немедленного
      // переавторизоваться — сначала убеждаемся, что он вообще не валиден,
      // и только потом идём в initData-вход (иначе двойной логин).
      const user = await apiFetch<User>('/me', { noReauth: true });
      set({ user, status: 'authed' });
      return;
    } catch (e) {
      // Токен протух (или отозван) — чистим и пробуем initData-вход ниже.
      writeStoredToken(null);
      set({ token: null, user: null });
      if (!(e instanceof ApiError) || e.status !== 401) {
        set({ status: 'error', error: e instanceof Error ? e.message : String(e) });
        return;
      }
    }
  }

  const initData = getInitData();
  if (!initData) {
    set({
      status: 'error',
      error: 'Откройте приложение из Telegram: без initData вход невозможен.',
    });
    return;
  }
  try {
    await get().login(initData);
  } catch (e) {
    set({ status: 'error', error: e instanceof Error ? e.message : String(e) });
  }
}

export const useAuthStore = create<AuthState>((set, get) => ({
  token: null,
  user: null,
  status: 'init',
  error: null,

  isAuthed: () => get().status === 'authed' && get().token !== null,

  setSession(session) {
    writeStoredToken(session.token);
    set({ token: session.token, user: session.user, status: 'authed', error: null });
  },

  async bootstrap() {
    if (bootstrapInFlight) return bootstrapInFlight;
    bootstrapInFlight = runBootstrap(set, get).finally(() => {
      bootstrapInFlight = null;
    });
    return bootstrapInFlight;
  },

  async login(initData) {
    const raw = initData ?? getInitData();
    if (!raw) throw new ApiError(0, 'no_init_data', 'Нет initData — откройте приложение из Telegram.');
    const session = await authenticate(raw);
    get().setSession(session);
  },

  async logout() {
    try {
      await apiFetch<void>('/me/logout', { method: 'POST', noReauth: true });
    } catch {
      // Даже если сервер не ответил — локально выходим.
    }
    writeStoredToken(null);
    // 'anonymous', а не 'error': осознанный выход не должен показывать экран
    // «Не удалось войти» с предложением повторить вход.
    set({ token: null, user: null, status: 'anonymous', error: null });
  },

  async patchMe(patch) {
    const user = await api.fetch<User>('/me', { method: 'PATCH', body: patch });
    set({ user });
  },
}));

// Связка api-клиента со стором: 401 → повторный вход по initData; провал
// повторного входа → экран ошибки (Task 13: api.ts не импортирует стор).
configureAuth({
  getToken: () => useAuthStore.getState().token,
  getInitData: () => getInitData() ?? null,
  onSession: (session) => useAuthStore.getState().setSession(session),
  canReauth: () => useAuthStore.getState().status !== 'anonymous',
  onAuthFailure: (message) => {
    // Фоновый 401 после выхода не должен перебивать нейтральный экран:
    // статус уже 'anonymous', и это осознанное состояние, а не сбой.
    if (useAuthStore.getState().status === 'anonymous') return;
    useAuthStore.setState({ status: 'error', error: message, token: null, user: null });
  },
});