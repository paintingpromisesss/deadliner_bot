// Экранный тест настроек (спека §9, экран 6): общий дефолт ЛС-дублей и
// per-group переопределения.
//
// Ключевая проверка — ТЕЛО PATCH: backend различает `dm_notify: false` и
// `dm_notify: null` (снятие переопределения), а «отсутствие поля» читает как
// «не трогать». Поэтому тест ловит именно отправленный JSON, а не факт вызова:
// подмена null на false молча оставила бы у группы своё «выключено» навсегда.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot } from '@telegram-apps/telegram-ui';

import { SettingsScreen } from './SettingsScreen';
import { configureAuth } from '../lib/api';
import { useAuthStore } from '../stores/auth';

interface Call {
  url: string;
  method: string;
  body: unknown;
}

let calls: Call[] = [];
let settingsPayload: unknown;

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const user = {
  id: 1,
  telegram_id: 42,
  username: 'durov',
  first_name: 'Pavel',
  tz: 'Europe/Moscow',
  dm_notify_default: true,
  is_superadmin: false,
};

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });

      if (url === '/api/v1/notifications/settings' && method === 'GET') {
        return json(200, settingsPayload);
      }
      if (url === '/api/v1/notifications/settings' && method === 'PATCH') {
        // Сервер возвращает настройки целиком; здесь достаточно эха запроса.
        return json(200, settingsPayload);
      }
      if (url === '/api/v1/me' && method === 'PATCH') {
        return json(200, { ...user, ...(body as object) });
      }
      return json(200, []);
    }),
  );
}

function renderScreen() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <AppRoot platform="base">
      <QueryClientProvider client={client}>
        <SettingsScreen />
      </QueryClientProvider>
    </AppRoot>,
  );
}

beforeEach(() => {
  calls = [];
  settingsPayload = {
    dm_notify_default: true,
    groups: [
      { group_id: 42, slug: 'М8О-401Б-23', title: 'М8О-401Б-23 (ИУ7)', dm_notify: true, override: true },
      { group_id: 43, slug: 'ИКБО-33-21', title: 'ИКБО-33-21', dm_notify: true, override: false },
    ],
  };
  configureAuth({ getToken: () => 'tok', getInitData: () => null, canReauth: () => true });
  useAuthStore.setState({ status: 'authed', token: 'tok', user });
  stubFetch();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('SettingsScreen: базовые секции', () => {
  it('показывает профиль, tz и общий дефолт (регрессия Task 13)', async () => {
    renderScreen();

    expect(await screen.findByText('Pavel')).toBeTruthy();
    expect(screen.getByText('@durov')).toBeTruthy();
    expect(screen.getByText('Дубли в личку по умолчанию')).toBeTruthy();
    // Кнопка сохранения заблокирована, пока ничего не изменено.
    const save = screen.getByRole('button', { name: 'Сохранить' }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });

  it('изменение tz включает сохранение и шлёт PATCH /me', async () => {
    renderScreen();

    await screen.findByText('Pavel');
    const select = screen.getByDisplayValue('Europe/Moscow') as HTMLSelectElement;
    await act(async () => {
      fireEvent.change(select, { target: { value: 'Asia/Yekaterinburg' } });
    });

    const save = screen.getByRole('button', { name: 'Сохранить' }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    await act(async () => {
      fireEvent.click(save);
      await Promise.resolve();
    });

    await waitFor(() => {
      const call = calls.find((c) => c.url === '/api/v1/me' && c.method === 'PATCH');
      expect((call?.body as { tz?: string })?.tz).toBe('Asia/Yekaterinburg');
    });
  });
});

describe('SettingsScreen: переопределения по группам', () => {
  it('показывает группы с признаком переопределения', async () => {
    renderScreen();

    const own = await screen.findByTestId('group-notify-42');
    expect(own.textContent).toContain('М8О-401Б-23');
    expect(own.textContent).toContain('своё значение');

    const inherited = screen.getByTestId('group-notify-43');
    expect(inherited.textContent).toContain('как по умолчанию');
  });

  it('кнопка «наследовать» есть только у переопределённой группы', async () => {
    renderScreen();

    await screen.findByTestId('group-notify-42');
    expect(screen.getByTestId('group-inherit-42')).toBeTruthy();
    expect(screen.queryByTestId('group-inherit-43')).toBeNull();
  });

  it('переключение группы шлёт PATCH {group_id, dm_notify:false}', async () => {
    renderScreen();

    const toggle = (await screen.findByTestId('group-notify-switch-42')) as HTMLInputElement;
    await act(async () => {
      fireEvent.click(toggle);
      await Promise.resolve();
    });

    await waitFor(() => {
      const call = calls.find(
        (c) => c.url === '/api/v1/notifications/settings' && c.method === 'PATCH',
      );
      expect(call?.body).toEqual({ group_id: 42, dm_notify: false });
    });
  });

  it('«наследовать» шлёт PATCH с dm_notify:null (снятие переопределения)', async () => {
    renderScreen();

    const inherit = await screen.findByTestId('group-inherit-42');
    await act(async () => {
      fireEvent.click(inherit);
      await Promise.resolve();
    });

    await waitFor(() => {
      const call = calls.find(
        (c) => c.url === '/api/v1/notifications/settings' && c.method === 'PATCH',
      );
      // Именно null, а не false: false оставил бы группе собственное значение.
      expect(call?.body).toEqual({ group_id: 42, dm_notify: null });
    });
  });

  it('переключение не задевает общий дефолт (нет PATCH /me)', async () => {
    renderScreen();

    const toggle = (await screen.findByTestId('group-notify-switch-43')) as HTMLInputElement;
    await act(async () => {
      fireEvent.click(toggle);
      await Promise.resolve();
    });

    await waitFor(() =>
      expect(
        calls.some((c) => c.url === '/api/v1/notifications/settings' && c.method === 'PATCH'),
      ).toBe(true),
    );
    expect(calls.some((c) => c.url === '/api/v1/me' && c.method === 'PATCH')).toBe(false);
  });

  it('пустой список групп — пояснение вместо секции', async () => {
    settingsPayload = { dm_notify_default: true, groups: [] };
    renderScreen();

    expect(await screen.findByTestId('group-notifications-empty')).toBeTruthy();
  });

  it('ошибка загрузки настроек показывается видимой строкой с повтором', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json(500, { error: { code: 'internal', message: 'Внутренняя ошибка' } })),
    );
    renderScreen();

    const retry = await screen.findByTestId('group-notifications-retry');
    expect(retry).toBeTruthy();
    expect(screen.getByText('Внутренняя ошибка')).toBeTruthy();
  });

  it('ошибка переключения не глотается (строка ошибки под секцией)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? 'GET';
        calls.push({ url, method, body: undefined });
        if (url === '/api/v1/notifications/settings' && method === 'GET') {
          return json(200, settingsPayload);
        }
        if (url === '/api/v1/notifications/settings' && method === 'PATCH') {
          return json(403, { error: { code: 'forbidden', message: 'Недостаточно прав' } });
        }
        return json(200, []);
      }),
    );
    renderScreen();

    const toggle = (await screen.findByTestId('group-notify-switch-42')) as HTMLInputElement;
    await act(async () => {
      fireEvent.click(toggle);
      await Promise.resolve();
    });

    const alert = await screen.findByTestId('group-notify-error');
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toContain('Недостаточно прав');
  });
});