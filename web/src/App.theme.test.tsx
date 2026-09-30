// Регрессионный тест I-1: TelegramUI обязан подписываться на 'themeChanged'.
//
// useAppearance.js кита при заданном пропе appearance делает
// `setAppearance(prop); return () => {}` — подписки нет, смена темы в Telegram
// игнорируется. Тест монтирует App целиком и проверяет, что после
// themeChanged корневой элемент переключается на тёмный/светлый класс, то есть
// подписка реально состоялась (если бы App передавал appearance, класс не
// изменился бы).
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from './App';
import { TOKEN_STORAGE_KEY } from './stores/auth';

/** Классы темы из styles.css telegram-ui (dark/light — цветовые наборы). */
const DARK_CLASS = 'tgui-865b921add8ee075';

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

/** Минимальный window.Telegram.WebApp: только то, что читает useAppearance. */
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

    // Подписка состоялась: при переданном пропе appearance её бы не было.
    expect(tg.listenerCount()).toBe(1);

    await tg.flipTheme('dark');
    expect(appRoot.className).toContain(DARK_CLASS);

    await tg.flipTheme('light');
    expect(appRoot.className).not.toContain(DARK_CLASS);
  });

  it('без пропа appearance начальная тема берётся из системной (matchMedia)', async () => {
    // telegram-ui при отсутствии пропа стартует от getInitialAppearance(),
    // то есть от prefers-color-scheme — так же, как в браузере. Тест
    // подтверждает, что мы не «приклеиваем» тему пропом.
    installFakeTelegram('light');
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({
        matches: true,
        addEventListener: () => {},
        removeEventListener: () => {},
      })),
    );

    const appRoot = await renderApp();
    expect(appRoot.className).toContain(DARK_CLASS);
  });
});