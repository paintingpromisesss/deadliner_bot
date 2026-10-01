// Экранный тест «Календарь»: сетка месяца, точки-маркеры на днях с
// дедлайнами, навигация по месяцам, тап по дню → список дня, запрос данных в
// границах месяца (from/to в tz пользователя).
//
// Время заморожено: 2026-09-29 12:00 MSK — сетка и подсветка «сегодня» не
// зависят от дня запуска тестов.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot } from '@telegram-apps/telegram-ui';

import { CalendarScreen } from './CalendarScreen';
import { configureAuth } from '../../lib/api';
import { useAuthStore } from '../../stores/auth';

const MSK = 'Europe/Moscow';
const NOW_ISO = '2026-09-29T09:00:00.000Z'; // 12:00 MSK

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

function deadline(id: number, dueAt: string, extra: Record<string, unknown> = {}) {
  return {
    id,
    group_id: null,
    owner_user_id: 1,
    created_by: 1,
    title: `Дедлайн ${id}`,
    description: '',
    due_at: dueAt,
    tz: MSK,
    status: 'active',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    ...extra,
  };
}

let deadlinesPayload: unknown[] = [];

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, method: init?.method ?? 'GET' });
      if (url.startsWith('/api/v1/groups')) return json(200, []);
      if (url.startsWith('/api/v1/me/deadlines')) return json(200, deadlinesPayload);
      return json(200, { deadline: deadlinesPayload[0], reminders: [] });
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
        <CalendarScreen />
      </QueryClientProvider>
    </AppRoot>,
  );
}

/** Последний запрос списка дедлайнов (для проверки границ месяца). */
function lastListUrl(): string {
  const urls = calls.filter((c) => c.url.startsWith('/api/v1/me/deadlines'));
  return urls[urls.length - 1]?.url ?? '';
}

beforeEach(() => {
  calls = [];
  deadlinesPayload = [];
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(new Date(NOW_ISO));
  configureAuth({ getToken: () => 'tok', getInitData: () => null, canReauth: () => true });
  setAuthUser(MSK);
  stubFetch();
});

/** Пользователь с указанным tz: от него зависят и границы месяца, и сетка дня. */
function setAuthUser(tz: string) {
  useAuthStore.setState({
    status: 'authed',
    token: 'tok',
    user: {
      id: 1,
      telegram_id: 42,
      username: 'durov',
      first_name: 'Pavel',
      tz,
      dm_notify_default: true,
      is_superadmin: false,
    },
  });
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('CalendarScreen: сетка месяца', () => {
  it('рисует текущий месяц и подсвечивает сегодня', async () => {
    renderScreen();

    expect(await screen.findByTestId('cal-title')).toBeTruthy();
    expect(screen.getByTestId('cal-title').textContent).toContain('2026');
    // Сентябрь 2026: первое — вторник, значит первая ячейка — 31 августа.
    expect(screen.getByTestId('day-2026-08-31')).toBeTruthy();
    expect(screen.getByTestId('day-2026-09-01')).toBeTruthy();
    expect(screen.getByTestId('day-2026-09-30')).toBeTruthy();
    // Заголовки дней недели, начиная с понедельника.
    expect(screen.getByText('пн')).toBeTruthy();
    expect(screen.getByText('вс')).toBeTruthy();
    // Сегодня — 29 сентября.
    expect(screen.getByTestId('day-2026-09-29').getAttribute('data-today')).toBe('true');
    expect(screen.getByTestId('day-2026-09-28').getAttribute('data-today')).toBe('false');
  });

  it('запрашивает дедлайны в границах месяца (MSK: 31.08 21:00Z — 30.09 20:59:59.999Z)', async () => {
    renderScreen();
    await waitFor(() => expect(lastListUrl()).not.toBe(''));

    const url = decodeURIComponent(lastListUrl());
    expect(url).toContain('from=2026-08-31T21:00:00.000Z');
    expect(url).toContain('to=2026-09-30T20:59:59.999Z');
    expect(url).toContain('scope=all');
  });

  it('точки-маркеры стоят только на днях с дедлайнами', async () => {
    deadlinesPayload = [
      deadline(1, '2026-09-29T15:00:00Z'), // 29.09 18:00 MSK
      deadline(2, '2026-09-29T18:00:00Z'), // 29.09 21:00 MSK (тот же день)
      deadline(3, '2026-09-10T09:00:00Z'), // 10.09 12:00 MSK
    ];
    renderScreen();

    await waitFor(() => {
      expect(screen.getByTestId('day-2026-09-29').getAttribute('data-count')).toBe('true');
    });
    expect(screen.getByTestId('day-2026-09-10').getAttribute('data-count')).toBe('true');
    expect(screen.getByTestId('day-2026-09-11').getAttribute('data-count')).toBe('false');
  });

  it('дедлайн в 23:00 UTC попадает в СЛЕДУЮЩИЙ локальный день', async () => {
    // 2026-09-29T21:00Z = 30.09 00:00 MSK → маркер на 30 сентября.
    deadlinesPayload = [deadline(1, '2026-09-29T21:00:00Z')];
    renderScreen();

    await waitFor(() => {
      expect(screen.getByTestId('day-2026-09-30').getAttribute('data-count')).toBe('true');
    });
    expect(screen.getByTestId('day-2026-09-29').getAttribute('data-count')).toBe('false');
  });
});

describe('CalendarScreen: навигация по месяцам', () => {
  it('«назад» перелистывает на август и перезапрашивает данные', async () => {
    renderScreen();
    await waitFor(() => expect(lastListUrl()).not.toBe(''));

    await act(async () => {
      fireEvent.click(screen.getByTestId('cal-prev'));
    });

    expect(screen.getByTestId('day-2026-08-01')).toBeTruthy();
    await waitFor(() => {
      expect(decodeURIComponent(lastListUrl())).toContain('from=2026-07-31T21:00:00.000Z');
    });
  });

  it('«вперёд» перелистывает на октябрь', async () => {
    renderScreen();
    await act(async () => {
      fireEvent.click(screen.getByTestId('cal-next'));
    });

    expect(screen.getByTestId('day-2026-10-01')).toBeTruthy();
    expect(screen.getByTestId('day-2026-10-31')).toBeTruthy();
    await waitFor(() => {
      expect(decodeURIComponent(lastListUrl())).toContain('from=2026-09-30T21:00:00.000Z');
    });
  });

  it('переход через границу года: декабрь → январь', async () => {
    renderScreen();
    // 3 шага вперёд: октябрь, ноябрь, декабрь.
    for (let i = 0; i < 3; i += 1) {
      await act(async () => {
        fireEvent.click(screen.getByTestId('cal-next'));
      });
    }
    expect(screen.getByTestId('day-2026-12-01')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('cal-next'));
    });
    expect(screen.getByTestId('day-2027-01-01')).toBeTruthy();
    expect(screen.getByTestId('cal-title').textContent).toContain('2027');
  });
});

describe('CalendarScreen: выбор дня', () => {
  it('до выбора дня список не показывается, есть подсказка', async () => {
    renderScreen();
    await screen.findByTestId('cal-title');
    expect(screen.queryByTestId('cell-1')).toBeNull();
    expect(screen.getByText(/Выберите другой день/)).toBeTruthy();
  });

  it('тап по дню с дедлайнами показывает список этого дня', async () => {
    deadlinesPayload = [
      deadline(1, '2026-09-29T15:00:00Z', { title: 'Второй по времени' }),
      deadline(2, '2026-09-29T12:00:00Z', { title: 'Первый по времени' }),
      deadline(3, '2026-09-30T09:00:00Z', { title: 'Другой день' }),
    ];
    renderScreen();

    await waitFor(() =>
      expect(screen.getByTestId('day-2026-09-29').getAttribute('data-count')).toBe('true'),
    );
    await act(async () => {
      fireEvent.click(screen.getByTestId('day-2026-09-29'));
    });

    expect(screen.getByTestId('day-2026-09-29').getAttribute('data-selected')).toBe('true');
    expect(screen.getByText('29 сентября')).toBeTruthy();
    // Дедлайны дня отсортированы по времени и не включают соседний день.
    expect(screen.getByTestId('cell-2')).toBeTruthy();
    expect(screen.getByTestId('cell-1')).toBeTruthy();
    expect(screen.queryByTestId('cell-3')).toBeNull();
    const list = screen.getByTestId('cell-2').parentElement!;
    expect(list.textContent!.indexOf('Первый по времени')).toBeLessThan(
      list.textContent!.indexOf('Второй по времени'),
    );
  });

  it('пустой день — подсказка, а не пустой список', async () => {
    deadlinesPayload = [deadline(1, '2026-09-10T09:00:00Z')];
    renderScreen();

    await waitFor(() =>
      expect(screen.getByTestId('day-2026-09-10').getAttribute('data-count')).toBe('true'),
    );
    await act(async () => {
      fireEvent.click(screen.getByTestId('day-2026-09-20'));
    });

    expect(screen.getByText('На этот день дедлайнов нет')).toBeTruthy();
    expect(screen.queryByTestId('cell-1')).toBeNull();
  });

  it('тап по дню соседнего месяца перелистывает календарь', async () => {
    renderScreen();
    await screen.findByTestId('day-2026-08-31');

    await act(async () => {
      fireEvent.click(screen.getByTestId('day-2026-08-31'));
    });

    // Август стал текущим месяцем, а выбранный день — 31.08.
    expect(screen.getByTestId('cal-title').textContent).toContain('август');
    expect(screen.getByTestId('day-2026-08-31').getAttribute('data-selected')).toBe('true');
    expect(screen.getByText('31 августа')).toBeTruthy();
  });

  it('тап по дедлайну дня открывает форму редактирования', async () => {
    deadlinesPayload = [deadline(1, '2026-09-29T15:00:00Z', { title: 'Правь меня' })];
    renderScreen();

    await waitFor(() =>
      expect(screen.getByTestId('day-2026-09-29').getAttribute('data-count')).toBe('true'),
    );
    await act(async () => {
      fireEvent.click(screen.getByTestId('day-2026-09-29'));
    });
    await act(async () => {
      fireEvent.click(within(screen.getByTestId('cell-1')).getByText('Правь меня'));
    });

    const title = (await screen.findByTestId('field-title')) as HTMLInputElement;
    expect(title.value).toBe('Правь меня');
  });

  it('«+» на выбранном дне предзаполняет дату формы', async () => {
    deadlinesPayload = [];
    renderScreen();

    await act(async () => {
      fireEvent.click(screen.getByTestId('day-2026-09-15'));
    });
    const add = await screen.findByTestId('day-add');
    await act(async () => {
      fireEvent.click(add);
    });

    await screen.findByTestId('deadline-sheet');
    expect((screen.getByTestId('field-date') as HTMLInputElement).value).toBe('2026-09-15');
  });
});

// Полдень UTC как «якорь» выбранного дня ломается в зонах UTC+12/+13: он
// приходится на следующее число, и подпись дня с предзаполнением формы уезжали
// на сутки вперёд от подсвеченной ячейки. Матрица зон пиннит это поведение.
describe.each([
  ['Europe/Moscow', '+3', '15 сентября'],
  ['Asia/Kamchatka', '+12', '15 сентября'],
  ['Pacific/Auckland', '+13', '15 сентября'],
  ['Asia/Yakutsk', '+9', '15 сентября'],
  ['Pacific/Honolulu', '-10', '15 сентября'],
])('CalendarScreen: выбранный день в tz %s (%s)', (tz, _offset, dayTitle) => {
  it('подсветка дня, заголовок списка и предзаполнение формы совпадают', async () => {
    setAuthUser(tz);
    deadlinesPayload = [];
    renderScreen();

    await screen.findByTestId('cal-title');
    await act(async () => {
      fireEvent.click(screen.getByTestId('day-2026-09-15'));
    });

    // Подсвечена ровно та ячейка, по которой тапнули...
    expect(screen.getByTestId('day-2026-09-15').getAttribute('data-selected')).toBe('true');
    // ...заголовок дня её подтверждает...
    expect(screen.getByText(dayTitle)).toBeTruthy();
    expect(screen.queryByText('16 сентября')).toBeNull();

    // ...и форма предзаполнена той же календарной датой.
    const add = await screen.findByTestId('day-add');
    await act(async () => {
      fireEvent.click(add);
    });
    await screen.findByTestId('deadline-sheet');
    expect((screen.getByTestId('field-date') as HTMLInputElement).value).toBe('2026-09-15');
  });

  it('границы месяца запрашиваются в этой же зоне', async () => {
    setAuthUser(tz);
    renderScreen();
    await waitFor(() => expect(lastListUrl()).not.toBe(''));

    // Начало месяца в настенном времени tz: 00:00 первого числа.
    const from = new Date(decodeURIComponent(lastListUrl()).match(/from=([^&]+)/)![1]).toISOString();
    const local = new Intl.DateTimeFormat('ru-RU', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
      timeZone: tz,
    }).format(new Date(from));
    expect(local.slice(0, 10)).toBe('01.09.2026');
    expect(local.slice(12)).toBe('00:00');
  });
});