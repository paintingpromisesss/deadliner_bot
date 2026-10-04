// Регрессионный тест: AppRoot подписывается на 'themeChanged', тема клиента
// Telegram применяется на ходу. Тест монтирует App целиком и проверяет
// переключение класса dl-theme-dark / dl-theme-light после события.
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from './App';
import { TOKEN_STORAGE_KEY } from './stores/auth';

const user = {
  id: 1,
  telegram_id: 42,
  username: 'durov',
  first_name: 'Pavel',
  tz: 'Europe/Moscow',
  dm_notify_default: true,
  is_superadmin: false,
};

type Handler = () => void;

/** Минимальный window.Telegram.WebApp: только то, что читает AppRoot. */
function installFakeTelegram(colorScheme: string) {
  const handlers = new Map<string, Handler[]>();
  let scheme = colorScheme;

  const webApp = {
    get colorScheme() {
      return scheme;
    },
    platform: 'android',
    initData: 'query_id=1&hash=abc',
    onEvent(event: string, handler: Handler) {
      const list = handlers.get(event) ?? [];
      list.push(handler);
      handlers.set(event, list);
    },
    offEvent(event: string, handler: Handler) {
      handlers.set(event, (handlers.get(event) ?? []).filter((h) => h !== handler));
    },
  };

  (window as unknown as { Telegram: unknown }).Telegram = { WebApp: webApp };

  return {
    /** Смена темы в клиенте Telegram: событие + новое значение colorScheme. */
    async flipTheme(next: string) {
      scheme = next;
      for (const handler of handlers.get('themeChanged') ?? []) await act(async () => handler());
    },
    listenerCount: () => (handlers.get('themeChanged') ?? []).length,
  };
}

let container: HTMLDivElement | null = null;
let root: ReturnType<typeof createRoot> | null = null;

beforeEach(() => {
  // Без этого React не считает окружение тестовым и act() предупреждает.
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  window.localStorage.setItem(TOKEN_STORAGE_KEY, 'stored-token');
  window.location.hash = '#/settings';
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify(user), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  );
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  if (root) act(() => root!.unmount());
  container?.remove();
  container = null;
  root = null;
  delete (window as unknown as { Telegram?: unknown }).Telegram;
  window.localStorage.clear();
  vi.unstubAllGlobals();
});

/** Ждём, пока bootstrap авторизации доведёт App до рабочего экрана. */
async function renderApp() {
  await act(async () => {
    root!.render(<App />);
  });
  return document.querySelector('.dl-root') as HTMLElement;
}

describe('тема Telegram подхватывается на лету (I-1)', () => {
  it('подписывается на themeChanged и переключает тёмный класс', async () => {
    const tg = installFakeTelegram('light');
    const appRoot = await renderApp();

    // Подписка состоялась: без слушателя смена темы молча игнорировалась бы.
    expect(tg.listenerCount()).toBe(1);

    await tg.flipTheme('dark');
    expect(appRoot.className).toContain('dl-theme-dark');

    await tg.flipTheme('light');
    expect(appRoot.className).toContain('dl-theme-light');
  });

  it('вне Telegram начальная тема берётся из системной (matchMedia)', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({
        matches: true,
        addEventListener: () => {},
        removeEventListener: () => {},
      })),
    );

    const appRoot = await renderApp();
    expect(appRoot.className).toContain('dl-theme-dark');
  });
});
