// Экранный тест «Дедлайны»: hero-карточка ближайшего дедлайна, чип-фильтры
// (Все/Личные/Групповые), сегменты по срокам и доступность действий
// «выполнить/удалить» прямо из списка.
//
// Системное время замораживается (vi.useFakeTimers + shouldAdvanceTime):
// сегменты «Сегодня/7 дней/Позже» зависят от текущих суток, и без фиксации
// тест падал бы в определённые часы. now = 2026-09-29 12:00 MSK.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot } from '@telegram-apps/telegram-ui';

import { DeadlinesScreen } from './DeadlinesScreen';
import { configureAuth } from '../../lib/api';
import { useAuthStore } from '../../stores/auth';

const MSK = 'Europe/Moscow';
/** Фиксированное «сейчас»: 2026-09-29 12:00 MSK (09:00 UTC). */
const NOW_ISO = '2026-09-29T09:00:00.000Z';
const DAY = 86_400_000;

interface Call {
  url: string;
  method: string;
}

let calls: Call[] = [];

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** Часы от фиксированного мгновения: сдвиг в миллисекундах (может быть отрицательным). */
function dueAt(offsetMs: number): string {
  return new Date(Date.parse(NOW_ISO) + offsetMs).toISOString();
}

function deadline(id: number, offsetMs: number, extra: Record<string, unknown> = {}) {
  return {
    id,
    group_id: null,
    owner_user_id: 1,
    created_by: 1,
    title: `Дедлайн ${id}`,
    description: '',
    due_at: dueAt(offsetMs),
    tz: MSK,
    status: 'active',
    created_at: dueAt(-30 * DAY),
    updated_at: dueAt(-30 * DAY),
    ...extra,
  };
}

const groupsPayload = [
  {
    group: {
      id: 42,
      slug: 'М8О-401Б-23',
      title: 'М8О-401Б-23',
      status: 'active',
      official: false,
      created_by: 1,
      default_presets: [10080, 4320, 1440],
      created_at: '2026-09-01T00:00:00Z',
      updated_at: '2026-09-01T00:00:00Z',
    },
    role: 'admin',
  },
];

/** Тот же состав, но роль участника: групповые дедлайны ему доступны только на чтение. */
const memberGroupsPayload = [{ ...groupsPayload[0], role: 'member' }];

function stubFetch(
  deadlines: unknown[],
  options: { fail?: boolean; groups?: unknown[]; failMutation?: boolean } = {},
) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ url, method });
      if (options.fail) {
        return json(500, { error: { code: 'internal', message: 'Внутренняя ошибка' } });
      }
      // Мутации падают отдельно от чтения: проверяем видимость ошибки действия.
      if (options.failMutation && method !== 'GET') {
        return json(403, { error: { code: 'forbidden', message: 'Недостаточно прав' } });
      }
      if (url.startsWith('/api/v1/groups')) return json(200, options.groups ?? groupsPayload);
      if (url.startsWith('/api/v1/me/deadlines')) return json(200, deadlines);
      return json(200, { deadline: deadlines[0], reminders: [] });
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
        <DeadlinesScreen />
      </QueryClientProvider>
    </AppRoot>,
  );
}

/** Кнопка «выполнить/удалить» внутри строки конкретного дедлайна. */
function cellActions(id: number): HTMLElement {
  return screen.getByTestId(`cell-${id}`);
}

beforeEach(() => {
  calls = [];
  // shouldAdvanceTime: таймеры идут вместе с реальным временем, поэтому waitFor
  // из testing-library продолжает работать при замоканных таймерах.
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(new Date(NOW_ISO));
  configureAuth({ getToken: () => 'tok', getInitData: () => null, canReauth: () => true });
  useAuthStore.setState({
    status: 'authed',
    token: 'tok',
    user: {
      id: 1,
      telegram_id: 42,
      username: 'durov',
      first_name: 'Pavel',
      tz: MSK,
      dm_notify_default: true,
      is_superadmin: false,
    },
  });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('DeadlinesScreen: hero-карточка', () => {
  it('показывает ближайший дедлайн с датой и обратным отсчётом', async () => {
    stubFetch([
      deadline(1, 30 * DAY),
      deadline(2, 2 * DAY + 3 * 3600_000, { title: 'Ближайшая' }),
      deadline(3, 20 * DAY),
    ]);
    renderScreen();

    await screen.findByTestId('hero-title');
    expect(screen.getByTestId('hero-title').textContent).toBe('Ближайшая');
    // Дата — в tz пользователя, формат дд.мм.гггг чч:мм: +2 дня 3 часа от
    // 12:00 MSK 29.09 → 01.10 15:00.
    expect(screen.getByTestId('hero-due').textContent).toBe('01.10.2026 15:00');
    expect(screen.getByTestId('hero-countdown').textContent).toBe('осталось 2 дня 3 часа');
  });

  it('просроченный дедлайн показывается как просроченный', async () => {
    stubFetch([deadline(1, -3 * 3600_000, { title: 'Просрочка' }), deadline(2, 10 * DAY)]);
    renderScreen();

    await screen.findByTestId('hero-countdown');
    expect(screen.getByTestId('hero-title').textContent).toBe('Просрочка');
    expect(screen.getByTestId('hero-countdown').textContent).toBe('просрочен на 3 часа');
  });

  it('пустой список — плейсхолдер с призывом создать дедлайн', async () => {
    stubFetch([]);
    renderScreen();

    expect(await screen.findByText('Дедлайнов нет')).toBeTruthy();
    expect(screen.queryByTestId('hero-title')).toBeNull();
  });

  it('ошибка загрузки — плейсхолдер и повтор запроса', async () => {
    stubFetch([], { fail: true });
    renderScreen();

    expect(await screen.findByText('Не удалось загрузить данные')).toBeTruthy();
    const before = calls.filter((c) => c.url.startsWith('/api/v1/me/deadlines')).length;
    await act(async () => {
      fireEvent.click(screen.getByText('Повторить'));
    });
    await waitFor(() => {
      expect(calls.filter((c) => c.url.startsWith('/api/v1/me/deadlines')).length).toBeGreaterThan(
        before,
      );
    });
  });
});

describe('DeadlinesScreen: сегменты и фильтры', () => {
  it('раскладывает дедлайны по сегментам Просрочено/Сегодня/7 дней/Позже', async () => {
    stubFetch([
      deadline(1, -3600_000), // 11:00 MSK — просрочен
      deadline(2, 9 * 3600_000), // 21:00 MSK тех же суток — сегодня
      deadline(3, 3 * DAY), // 02.10 — 7 дней
      deadline(4, 40 * DAY), // 08.11 — позже
    ]);
    renderScreen();

    expect(await screen.findByTestId('segment-overdue')).toBeTruthy();
    expect(screen.getByTestId('segment-today')).toBeTruthy();
    expect(screen.getByTestId('segment-week')).toBeTruthy();
    expect(screen.getByTestId('segment-later')).toBeTruthy();

    expect(within(screen.getByTestId('segment-overdue')).getByTestId('cell-1')).toBeTruthy();
    expect(within(screen.getByTestId('segment-today')).getByTestId('cell-2')).toBeTruthy();
    expect(within(screen.getByTestId('segment-week')).getByTestId('cell-3')).toBeTruthy();
    expect(within(screen.getByTestId('segment-later')).getByTestId('cell-4')).toBeTruthy();
    // Просроченная строка помечена атрибутом (destructive-цвет в CSS).
    expect(screen.getByTestId('cell-1').getAttribute('data-overdue')).toBe('true');
    expect(screen.getByTestId('cell-3').getAttribute('data-overdue')).toBe('false');
  });

  it('пустые сегменты не рендерятся (нет заголовка секции)', async () => {
    stubFetch([deadline(1, 40 * DAY)]);
    renderScreen();

    await screen.findByTestId('segment-later');
    expect(screen.queryByTestId('segment-overdue')).toBeNull();
    expect(screen.queryByTestId('segment-today')).toBeNull();
    expect(screen.queryByTestId('segment-week')).toBeNull();
  });

  it('чип «Личные» скрывает групповые, «Групповые» — личные', async () => {
    stubFetch([
      deadline(1, 3 * DAY, { title: 'Личная' }),
      deadline(2, 4 * DAY, { title: 'Групповая', group_id: 42, owner_user_id: null }),
    ]);
    renderScreen();

    await screen.findByTestId('cell-1');
    expect(screen.getByTestId('cell-2')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Личные' }));
    });
    expect(screen.getByTestId('cell-1')).toBeTruthy();
    expect(screen.queryByTestId('cell-2')).toBeNull();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Групповые' }));
    });
    expect(screen.queryByTestId('cell-1')).toBeNull();
    expect(screen.getByTestId('cell-2')).toBeTruthy();
  });

  it('фильтр без подходящих дедлайнов — подсказка «Ничего не найдено»', async () => {
    stubFetch([deadline(1, 3 * DAY, { title: 'Личная' })]);
    renderScreen();

    await screen.findByTestId('cell-1');
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Групповые' }));
    });
    expect(screen.getByText('Ничего не найдено')).toBeTruthy();
  });

  it('подпись группового дедлайна содержит слаг группы', async () => {
    stubFetch([deadline(1, 3 * DAY, { group_id: 42, owner_user_id: null, title: 'Групповая' })]);
    renderScreen();

    await screen.findByTestId('cell-1');
    await waitFor(() => {
      expect(screen.getByTestId('cell-1').textContent).toContain('М8О-401Б-23');
    });
  });
});

describe('DeadlinesScreen: действия из списка', () => {
  it('кнопка «выполнить» шлёт POST /deadlines/{id}/complete', async () => {
    stubFetch([deadline(7, 3 * DAY)]);
    renderScreen();

    await screen.findByTestId('complete-7');
    await act(async () => {
      fireEvent.click(screen.getByTestId('complete-7'));
    });
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/deadlines/7/complete' && c.method === 'POST')).toBe(
        true,
      ),
    );
  });

  it('кнопка «удалить» требует подтверждения и затем шлёт DELETE /deadlines/{id}', async () => {
    stubFetch([deadline(8, 3 * DAY)]);
    renderScreen();

    await screen.findByTestId('delete-8');
    await act(async () => {
      fireEvent.click(screen.getByTestId('delete-8'));
    });
    // Случайный тап не должен удалять: сначала подтверждение.
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    expect(screen.getByText('Удалить дедлайн?')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-delete-8'));
    });
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/deadlines/8' && c.method === 'DELETE')).toBe(true),
    );
  });

  it('отмена в подтверждении не удаляет дедлайн', async () => {
    stubFetch([deadline(8, 3 * DAY)]);
    renderScreen();

    await screen.findByTestId('delete-8');
    await act(async () => {
      fireEvent.click(screen.getByTestId('delete-8'));
    });
    await act(async () => {
      fireEvent.click(screen.getByText('Отмена'));
    });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('FAB «+» открывает форму создания', async () => {
    stubFetch([deadline(1, 3 * DAY)]);
    renderScreen();

    await screen.findByTestId('fab-add');
    await act(async () => {
      fireEvent.click(screen.getByTestId('fab-add'));
    });
    expect(await screen.findByTestId('deadline-sheet')).toBeTruthy();
    expect(screen.getByText('Новый дедлайн')).toBeTruthy();
  });

  it('тап по строке открывает форму редактирования с предзаполнением', async () => {
    stubFetch([deadline(9, 3 * DAY, { title: 'Правь меня' })]);
    renderScreen();

    await screen.findByTestId('cell-9');
    await act(async () => {
      fireEvent.click(within(cellActions(9)).getByText('Правь меня'));
    });

    const title = (await screen.findByTestId('field-title')) as HTMLInputElement;
    expect(title.value).toBe('Правь меня');
    expect(screen.getByTestId('sheet-complete')).toBeTruthy();
    // Срок подставляется в tz пользователя: 02.10.2026 12:00 MSK.
    expect((screen.getByTestId('field-date') as HTMLInputElement).value).toBe('2026-10-02');
    expect((screen.getByTestId('field-time') as HTMLInputElement).value).toBe('12:00');
  });
});

// Backend требуют admin для complete/delete группового дедлайна (requireWrite):
// участнику кнопки не показываются вовсе, а если мутация всё же упала — об этом
// сообщается видимой ошибкой, а не молчанием.
describe('DeadlinesScreen: права на действия', () => {
  it('участник группы не видит кнопок на групповом дедлайне', async () => {
    stubFetch(
      [
        deadline(1, 3 * DAY, { title: 'Личная' }),
        // Автор группового — другой пользователь (999): проверяем именно
        // «участник, НЕ автор» — автор группового писать вправе (спека §5.2).
        deadline(2, 4 * DAY, {
          title: 'Групповая',
          group_id: 42,
          owner_user_id: null,
          created_by: 999,
        }),
      ],
      { groups: memberGroupsPayload },
    );
    renderScreen();

    await screen.findByTestId('cell-2');
    // Личный доступен владельцу, групповой — нет.
    expect(screen.getByTestId('complete-1')).toBeTruthy();
    expect(screen.getByTestId('delete-1')).toBeTruthy();
    expect(screen.queryByTestId('complete-2')).toBeNull();
    expect(screen.queryByTestId('delete-2')).toBeNull();
    // Строка при этом не пропала — она лишь без действий.
    expect(within(screen.getByTestId('cell-2')).getByText('Групповая')).toBeTruthy();
  });

  it('админ группы видит кнопки на групповом дедлайне', async () => {
    stubFetch([deadline(2, 4 * DAY, { title: 'Групповая', group_id: 42, owner_user_id: null })]);
    renderScreen();

    await screen.findByTestId('cell-2');
    expect(screen.getByTestId('complete-2')).toBeTruthy();
    expect(screen.getByTestId('delete-2')).toBeTruthy();
  });

  it('роль появляется после загрузки групп (до этого действий нет — не мигаем 403)', async () => {
    // Группы грузятся отдельным запросом: пока роль неизвестна, кнопки не
    // показываем. Это осознанно консервативно: показать и затем убрать хуже.
    // Дедлайн чужой (created_by 999), иначе автор видел бы действия сразу же —
    // право автора не зависит от загрузки роли.
    stubFetch([deadline(2, 4 * DAY, { group_id: 42, owner_user_id: null, created_by: 999 })], {
      groups: memberGroupsPayload,
    });
    renderScreen();

    await screen.findByTestId('cell-2');
    await waitFor(() => expect(calls.some((c) => c.url === '/api/v1/groups')).toBe(true));
    expect(screen.queryByTestId('complete-2')).toBeNull();
  });

  it('автор-участник видит кнопки на своём групповом дедлайне и открывает форму', async () => {
    // Спека §5.2 «автор/admin»: автор группового дедлайна сохраняет право
    // записи, даже будучи рядовым участником (смена старосты — штатный
    // сценарий §3.1: дедлайн остаётся редактируемым у его создателя).
    stubFetch([deadline(3, 4 * DAY, { title: 'Авторская', group_id: 42, owner_user_id: null })], {
      groups: memberGroupsPayload,
    });
    renderScreen();

    await screen.findByTestId('cell-3');
    expect(screen.getByTestId('complete-3')).toBeTruthy();
    expect(screen.getByTestId('delete-3')).toBeTruthy();

    // Форма правки открывается и тоже даёт действия (единый предикат).
    await act(async () => {
      fireEvent.click(within(cellActions(3)).getByText('Авторская'));
    });
    expect(((await screen.findByTestId('field-title')) as HTMLInputElement).value).toBe('Авторская');
    expect(screen.getByTestId('sheet-complete')).toBeTruthy();
  });

  it('ошибка мутации из списка показывается видимой строкой, а не проглатывается', async () => {
    stubFetch([deadline(7, 3 * DAY)], { failMutation: true });
    renderScreen();

    await screen.findByTestId('complete-7');
    await act(async () => {
      fireEvent.click(screen.getByTestId('complete-7'));
    });

    const alert = await screen.findByTestId('action-error');
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toBe('Недостаточно прав');
  });

  it('ошибка удаления из подтверждения тоже видна', async () => {
    stubFetch([deadline(8, 3 * DAY)], { failMutation: true });
    renderScreen();

    await screen.findByTestId('delete-8');
    await act(async () => {
      fireEvent.click(screen.getByTestId('delete-8'));
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-delete-8'));
    });

    expect((await screen.findByTestId('action-error')).textContent).toBe('Недостаточно прав');
  });
});