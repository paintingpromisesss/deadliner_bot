// Тест формы дедлайна на уровне рендера (testing-library): инлайн-валидация,
// блокировка отправки, предвыбор пресетов группы, режим правки с действиями
// «выполнить»/«удалить» и подтверждением удаления.
//
// Сеть мокается на уровне fetch (как в auth.test.ts) — проверяем реальный путь
// apiFetch → TanStack Query → компонент.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot } from '@telegram-apps/telegram-ui';

import { DeadlineSheet } from './DeadlineSheet';
import { configureAuth } from '../lib/api';
import { useAuthStore } from '../stores/auth';
import type { Deadline } from '../lib/deadlines';

const MSK = 'Europe/Moscow';

/** Группы пользователя: одна с админской ролью (можно писать дедлайны). */
const groupsPayload = [
  {
    group: {
      id: 42,
      slug: 'М8О-401Б-23',
      title: 'М8О-401Б-23',
      status: 'active',
      official: false,
      created_by: 1,
      default_presets: [10080, 1440],
      created_at: '2026-09-01T00:00:00Z',
      updated_at: '2026-09-01T00:00:00Z',
    },
    role: 'admin',
  },
];

function jsonResponse(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface Call {
  url: string;
  method: string;
  body: unknown;
}

let calls: Call[] = [];

function stubFetch(handler?: (call: Call) => Response | undefined) {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    };
    calls.push(call);
    return (
      handler?.(call) ??
      jsonResponse(200, { deadline: deadlineFixture, reminders: [] })
    );
  });
  vi.stubGlobal('fetch', fn);
  return fn;
}

const deadlineFixture = {
  id: 5,
  group_id: null,
  owner_user_id: 1,
  created_by: 1,
  title: 'Курсовая',
  description: 'детали',
  due_at: '2026-12-31T20:59:00Z',
  tz: MSK,
  status: 'active',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
} satisfies Deadline;

function renderSheet(props: Partial<React.ComponentProps<typeof DeadlineSheet>> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <AppRoot platform="base">
      <QueryClientProvider client={client}>
        <DeadlineSheet
          open
          onOpenChange={() => {}}
          deadline={null}
          tz={MSK}
          {...props}
        />
      </QueryClientProvider>
    </AppRoot>,
  );
}

/** Поле ввода внутри TelegramUI Input (header + input внутри label). */
function field(testId: string): HTMLInputElement {
  return screen.getByTestId(testId) as HTMLInputElement;
}

beforeEach(() => {
  calls = [];
  configureAuth({ getToken: () => 'token', getInitData: () => 'init', canReauth: () => true });
  useAuthStore.setState({
    status: 'authed',
    token: 'token',
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
  stubFetch((call) => {
    if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
    return undefined;
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('DeadlineSheet: валидация и отправка', () => {
  it('вне Telegram показывает собственную кнопку submit вместо MainButton', async () => {
    renderSheet();
    expect(await screen.findByTestId('sheet-submit')).toBeTruthy();
  });

  it('пустая форма: кнопка отправки заблокирована, ошибки не показаны до попытки', async () => {
    renderSheet();
    const submit = (await screen.findByTestId('sheet-submit')) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    expect(screen.queryByTestId('error-title')).toBeNull();
  });

  it('на blur пустого заголовка всплывает инлайн-ошибка (кнопка disabled — жмём не форму)', async () => {
    renderSheet();
    // Нативный disabled не даёт отправить: состояние ошибки показывается после
    // первого взаимодействия с полем (touched) — проверяем этот путь.
    fireEvent.blur(field('field-title'));
    expect(await screen.findByTestId('error-title')).toBeTruthy();
    expect(screen.getByTestId('error-title').textContent).toBe('Введите заголовок');
  });

  it('заголовок и будущая дата включают отправку, тело уходит в POST /deadlines', async () => {
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
      if (call.method === 'POST' && call.url === '/api/v1/deadlines') {
        return jsonResponse(201, { deadline: deadlineFixture, reminders: [] });
      }
      return undefined;
    });
    renderSheet();

    fireEvent.change(field('field-title'), { target: { value: 'Задача' } });
    // Дата в будущем относительно тестового «сейчас» (системное время).
    const future = new Date(Date.now() + 3 * 86_400_000).toISOString().slice(0, 10);
    fireEvent.change(field('field-date'), { target: { value: future } });

    const submit = (await screen.findByTestId('sheet-submit')) as HTMLButtonElement;
    await waitFor(() => expect(submit.disabled).toBe(false));

    await act(async () => {
      fireEvent.click(submit);
    });

    await waitFor(() => {
      expect(calls.some((c) => c.method === 'POST' && c.url === '/api/v1/deadlines')).toBe(true);
    });
    const post = calls.find((c) => c.method === 'POST')!;
    const body = post.body as Record<string, unknown>;
    expect(body.title).toBe('Задача');
    expect(body.group_id).toBeNull();
    expect(body.tz).toBe(MSK);
    expect(typeof body.due_at).toBe('string');
  });

  it('прошедший срок: ошибка «срок уже прошёл» и отправка заблокирована', async () => {
    renderSheet();
    fireEvent.change(field('field-title'), { target: { value: 'Задача' } });
    fireEvent.change(field('field-date'), { target: { value: '2020-01-01' } });
    fireEvent.change(field('field-time'), { target: { value: '10:00' } });

    expect(await screen.findByTestId('error-due')).toBeTruthy();
    expect((screen.getByTestId('sheet-submit') as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('DeadlineSheet: группы и пресеты', () => {
  it('список групп берётся из GET /groups (только админские)', async () => {
    renderSheet();
    await waitFor(() => {
      expect(calls.some((c) => c.url === '/api/v1/groups')).toBe(true);
    });
    await screen.findByText(/М8О-401Б-23/);
  });

  it('выбор группы включает пресеты группы (7д и 24ч, без 3д) и требует напоминания', async () => {
    renderSheet();
    await screen.findByText(/М8О-401Б-23/);

    const select = field('field-group');
    fireEvent.change(select, { target: { value: '42' } });

    // Пресеты группы: default_presets = [10080, 1440] → чипы 7д и 24ч активны.
    await waitFor(() => {
      expect(screen.getByTestId('preset-10080').getAttribute('data-selected')).toBe('true');
    });
    expect(screen.getByTestId('preset-1440').getAttribute('data-selected')).toBe('true');
    expect(screen.getByTestId('preset-4320').getAttribute('data-selected')).toBe('false');
  });

  it('снятие всех пресетов у группового дедлайна даёт ошибку про напоминания', async () => {
    renderSheet();
    await screen.findByText(/М8О-401Б-23/);
    fireEvent.change(field('field-group'), { target: { value: '42' } });
    await waitFor(() => {
      expect(screen.getByTestId('preset-10080').getAttribute('data-selected')).toBe('true');
    });

    fireEvent.click(screen.getByTestId('preset-10080'));
    fireEvent.click(screen.getByTestId('preset-1440'));

    expect(await screen.findByTestId('error-reminders')).toBeTruthy();
  });

  it('возврат к личному типу очищает пресеты группы', async () => {
    renderSheet();
    await screen.findByText(/М8О-401Б-23/);

    fireEvent.change(field('field-group'), { target: { value: '42' } });
    await waitFor(() => {
      expect(screen.getByTestId('preset-10080').getAttribute('data-selected')).toBe('true');
    });

    // Личный: пресеты группы в форме остаться не должны — иначе они ушли бы в
    // тело POST как напоминания личного дедлайна.
    fireEvent.change(field('field-group'), { target: { value: '' } });
    await waitFor(() => {
      expect(screen.getByTestId('preset-10080').getAttribute('data-selected')).toBe('false');
    });
    expect(screen.getByTestId('preset-1440').getAttribute('data-selected')).toBe('false');
    expect(screen.queryByTestId('error-reminders')).toBeNull();
  });
});

describe('DeadlineSheet: режим правки', () => {
  it('поля предзаполнены в tz пользователя, доступны «выполнить» и «удалить»', async () => {
    renderSheet({ deadline: deadlineFixture });

    await waitFor(() => {
      expect(field('field-title').value).toBe('Курсовая');
    });
    // 2026-12-31T20:59Z → 23:59 MSK 31 декабря.
    expect(field('field-date').value).toBe('2026-12-31');
    expect(field('field-time').value).toBe('23:59');
    expect(screen.getByTestId('sheet-complete')).toBeTruthy();
    expect(screen.getByTestId('sheet-delete')).toBeTruthy();
    // Тип дедлайна неизменяем: селект групп заблокирован в режиме правки.
    expect(field('field-group').disabled).toBe(true);
  });

  it('PATCH отправляет только поля дедлайна (без reminders/group_id)', async () => {
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
      if (call.method === 'PATCH') return jsonResponse(200, { deadline: deadlineFixture, reminders: [] });
      return undefined;
    });
    renderSheet({ deadline: deadlineFixture });

    await waitFor(() => expect(field('field-title').value).toBe('Курсовая'));
    fireEvent.change(field('field-title'), { target: { value: 'Курсовая v2' } });

    const submit = (await screen.findByTestId('sheet-submit')) as HTMLButtonElement;
    await waitFor(() => expect(submit.disabled).toBe(false));
    await act(async () => {
      fireEvent.click(submit);
    });

    await waitFor(() => expect(calls.some((c) => c.method === 'PATCH')).toBe(true));
    const patch = calls.find((c) => c.method === 'PATCH')!;
    expect(patch.url).toBe('/api/v1/deadlines/5');
    expect(patch.body).toMatchObject({ title: 'Курсовая v2', tz: MSK });
    expect(Object.keys(patch.body as object)).not.toContain('reminders');
  });

  it('«выполнить» дёргает POST /deadlines/{id}/complete', async () => {
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
      if (call.url === '/api/v1/deadlines/5/complete') {
        return jsonResponse(200, { deadline: { ...deadlineFixture, status: 'done' }, reminders: [] });
      }
      return undefined;
    });
    renderSheet({ deadline: deadlineFixture });

    await screen.findByTestId('sheet-complete');
    await act(async () => {
      fireEvent.click(screen.getByTestId('sheet-complete'));
    });
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/deadlines/5/complete' && c.method === 'POST')).toBe(
        true,
      ),
    );
  });

  it('удаление требует подтверждения и шлёт DELETE', async () => {
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
      if (call.method === 'DELETE') return jsonResponse(204, null);
      return undefined;
    });
    renderSheet({ deadline: deadlineFixture });

    await screen.findByTestId('sheet-delete');
    // DELETE уходит только после подтверждения.
    fireEvent.click(screen.getByTestId('sheet-delete'));
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    expect(screen.getByText('Удалить дедлайн?')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-delete'));
    });
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
    expect(calls.find((c) => c.method === 'DELETE')!.url).toBe('/api/v1/deadlines/5');
  });

  it('ошибка сервера показывается в форме', async () => {
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
      if (call.method === 'PATCH') {
        return jsonResponse(400, { error: { code: 'validation', message: 'Срок в прошлом' } });
      }
      return undefined;
    });
    renderSheet({ deadline: deadlineFixture });

    await waitFor(() => expect(field('field-title').value).toBe('Курсовая'));
    const submit = (await screen.findByTestId('sheet-submit')) as HTMLButtonElement;
    await waitFor(() => expect(submit.disabled).toBe(false));
    await act(async () => {
      fireEvent.click(submit);
    });

    expect(await screen.findByTestId('error-submit')).toBeTruthy();
    expect(screen.getByTestId('error-submit').textContent).toBe('Срок в прошлом');
  });

  it('участник группы не видит «выполнить»/«удалить» у группового дедлайна', async () => {
    // Автор — другой пользователь (999): тест про «участник, НЕ автор».
    // Иначе авторский доступ (спека §5.2) делал бы кнопки законными.
    const groupDeadline = { ...deadlineFixture, group_id: 42, owner_user_id: null, created_by: 999 };
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) {
        return jsonResponse(200, [{ ...groupsPayload[0], role: 'member' }]);
      }
      if (call.url === '/api/v1/deadlines/5') {
        return jsonResponse(200, { deadline: groupDeadline, reminders: [] });
      }
      return undefined;
    });
    renderSheet({ deadline: groupDeadline as Deadline });

    // Поля заполнены, но действий записи в форме нет: backend ответил бы 403
    // (requireWrite — автор дедлайна, админ группы или супер-админ).
    await waitFor(() => expect(field('field-title').value).toBe('Курсовая'));
    expect(screen.queryByTestId('sheet-complete')).toBeNull();
    expect(screen.queryByTestId('sheet-delete')).toBeNull();
  });

  it('автор-участник видит «выполнить»/«удалить» на своём групповом дедлайне', async () => {
    // Спека §5.2 «автор/admin»: рядовой участник — автор дедлайна, права записи
    // у него есть (created_by совпадает с me), хотя роль в группе — member.
    const authored = { ...deadlineFixture, group_id: 42, owner_user_id: null, created_by: 1 };
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) {
        return jsonResponse(200, [{ ...groupsPayload[0], role: 'member' }]);
      }
      if (call.url === '/api/v1/deadlines/5') {
        return jsonResponse(200, { deadline: authored, reminders: [] });
      }
      return undefined;
    });
    renderSheet({ deadline: authored as Deadline });

    expect(await screen.findByTestId('sheet-complete')).toBeTruthy();
    expect(screen.getByTestId('sheet-delete')).toBeTruthy();
  });

  it('супер-админ видит действия и в чужой группе', async () => {
    const groupDeadline = { ...deadlineFixture, group_id: 42, owner_user_id: null, created_by: 999 };
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) {
        return jsonResponse(200, [{ ...groupsPayload[0], role: 'member' }]);
      }
      if (call.url === '/api/v1/deadlines/5') {
        return jsonResponse(200, { deadline: groupDeadline, reminders: [] });
      }
      return undefined;
    });
    useAuthStore.setState({
      status: 'authed',
      token: 'token',
      user: {
        id: 1,
        telegram_id: 42,
        username: 'root',
        first_name: 'Root',
        tz: MSK,
        dm_notify_default: true,
        is_superadmin: true,
      },
    });
    renderSheet({ deadline: groupDeadline as Deadline });

    expect(await screen.findByTestId('sheet-complete')).toBeTruthy();
    expect(screen.getByTestId('sheet-delete')).toBeTruthy();
  });
});

describe('DeadlineSheet: напоминания в режиме правки', () => {
  it('чипы-пресеты скрыты: PATCH их не принимает (backend перегенерирует сам)', async () => {
    renderSheet({ deadline: deadlineFixture });

    await screen.findByTestId('sheet-complete');
    expect(screen.queryByTestId('preset-10080')).toBeNull();
    expect(screen.queryByTestId('add-reminder')).toBeNull();
    expect(screen.getByText('Напоминания пересчитываются автоматически при смене срока.')).toBeTruthy();
  });

  it('существующие напоминания грузятся из GET /deadlines/{id} со статусами', async () => {
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
      if (call.url === '/api/v1/deadlines/5') {
        return jsonResponse(200, {
          deadline: deadlineFixture,
          reminders: [
            {
              id: 1,
              kind: 'preset',
              offset_minutes: 10080,
              fire_at: '2026-12-24T20:59:00Z',
              status: 'pending',
            },
            {
              id: 2,
              kind: 'custom_at',
              fire_at: '2026-12-30T10:00:00Z',
              status: 'sent',
            },
          ],
        });
      }
      return undefined;
    });
    renderSheet({ deadline: deadlineFixture });

    expect(await screen.findByText('за 7 дн.')).toBeTruthy();
    expect(screen.getByText('ожидает')).toBeTruthy();
    // Точное напоминание показывается датой в tz пользователя.
    expect(screen.getByText('30.12.2026 13:00')).toBeTruthy();
    expect(screen.getByText('отправлено')).toBeTruthy();
    expect(calls.some((c) => c.url === '/api/v1/deadlines/5')).toBe(true);
  });

  it('нет напоминаний — подсказка, а не пустой блок', async () => {
    stubFetch((call) => {
      if (call.url.startsWith('/api/v1/groups')) return jsonResponse(200, groupsPayload);
      if (call.url === '/api/v1/deadlines/5') return jsonResponse(200, { deadline: deadlineFixture, reminders: [] });
      return undefined;
    });
    renderSheet({ deadline: deadlineFixture });

    expect(await screen.findByText('Напоминаний нет')).toBeTruthy();
  });
});

describe('DeadlineSheet: кастомные напоминания', () => {
  it('некорректное кастомное напоминание видно в форме и блокирует submit', async () => {
    const { container } = renderSheet();
    fireEvent.change(field('field-title'), { target: { value: 'Задача' } });
    const future = new Date(Date.now() + 3 * 86_400_000).toISOString().slice(0, 10);
    fireEvent.change(field('field-date'), { target: { value: future } });

    // До добавления напоминаний форма валидна (личный дедлайн).
    const submit = (await screen.findByTestId('sheet-submit')) as HTMLButtonElement;
    await waitFor(() => expect(submit.disabled).toBe(false));

    await act(async () => {
      fireEvent.click(screen.getByTestId('add-reminder'));
    });

    // Отступ 0 часов даёт 0 минут — меньше минимума (5): раньше такой элемент
    // молча выпадал из тела запроса, теперь это видимая ошибка, а отправка
    // заблокирована.
    const custom = () => container.querySelector('.dl-custom') as HTMLElement;
    const amount = custom().querySelector('input[type="number"]') as HTMLInputElement;
    fireEvent.change(amount, { target: { value: '0' } });

    expect(await screen.findByTestId('error-custom-reminders')).toBeTruthy();
    expect(submit.disabled).toBe(true);

    // Удаляем некорректное напоминание — ошибка уходит, submit снова доступен.
    await act(async () => {
      fireEvent.click(within(custom()).getByText('Удалить напоминание'));
    });
    await waitFor(() => expect(screen.queryByTestId('error-custom-reminders')).toBeNull());
    expect(submit.disabled).toBe(false);
  });
});