// Экранный тест группы и админ-панели (спека §9, экраны 5 и 7): гейт по роли
// (участник не видит админ-панель), статус привязки чата, состав участников с
// promote/demote/kick, инвайты с кодом «один раз», модерация дедлайнов и danger zone.
//
// Гейт — не косметика: backend отвергает админские вызовы 403, и показанная
// участнику кнопка означала бы гарантированный отказ. Поэтому проверяем именно
// ОТСУТСТВИЕ админских элементов, а не их отключённость.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot } from '../../components/ui';

import { GroupDetailScreen } from './GroupDetailScreen';
import { configureAuth } from '../../lib/api';
import { useAuthStore } from '../../stores/auth';

interface Call {
  url: string;
  method: string;
  body: unknown;
}

let calls: Call[] = [];

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const group = {
  id: 42,
  slug: 'М8О-401Б-23',
  title: 'М8О-401Б-23 (ИУ7)',
  status: 'active',
  official: false,
  created_by: 1,
  default_presets: [10080, 4320, 1440],
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
};

const members = [
  { user_id: 1, username: 'durov', first_name: 'Pavel', role: 'admin', joined_at: '2026-09-01T00:00:00Z' },
  { user_id: 2, username: 'ivanov', first_name: 'Иван', role: 'member', joined_at: '2026-09-02T00:00:00Z' },
];

interface Handlers {
  role?: string;
  binding?: unknown;
  members?: unknown[];
  /** Дедлайны на модерации (GET /groups/42/deadlines/pending). */
  pendingDeadlines?: unknown[];
  memberRole?: [number, unknown];
  kick?: number;
  invite?: [number, unknown];
}

let handlers: Handlers = {};

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url, method, body });

      if (url === '/api/v1/groups/42') {
        return json(200, {
          group,
          role: handlers.role ?? 'admin',
          binding: handlers.binding === undefined ? { chat_id: -100, chat_title: 'ИУ7 401Б' } : handlers.binding,
          members_count: (handlers.members ?? members).length,
        });
      }
      if (url === '/api/v1/groups/42/members' && method === 'GET') {
        return json(200, handlers.members ?? members);
      }
      if (url === '/api/v1/groups/42/members/2' && method === 'PATCH') {
        const [status, payload] = handlers.memberRole ?? [200, { user_id: 2, role: 'admin' }];
        return json(status, status >= 400 ? { error: { code: 'last_admin', message: 'последний админ' } } : payload);
      }
      if (url === '/api/v1/groups/42/members/2' && method === 'DELETE') {
        const status = handlers.kick ?? 204;
        return json(status, status >= 400 ? { error: { code: 'forbidden', message: 'Нельзя' } } : null);
      }
      if (url === '/api/v1/groups/42/me' && method === 'DELETE') return json(204, null);
      if (url === '/api/v1/groups/42' && method === 'DELETE') return json(204, null);
      if (url === '/api/v1/groups/42/invites' && method === 'POST') {
        const [status, payload] = handlers.invite ?? [201, { code: 'ABCD2345', expires_at: '2026-10-08T12:00:00Z' }];
        return json(status, status >= 400 ? { error: { code: 'validation', message: 'Неверно' } } : payload);
      }
      if (url.startsWith('/api/v1/groups/42/invites/') && method === 'DELETE') return json(204, null);
      if (url === '/api/v1/groups/42/deadlines/pending') return json(200, handlers.pendingDeadlines ?? []);
      if (url === '/api/v1/deadlines/7/approve') return json(200, { deadline: { id: 7, status: 'active' }, reminders: [] });
      if (url === '/api/v1/deadlines/7/reject') return json(200, { deadline: { id: 7, status: 'rejected' }, reminders: [] });
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
        <GroupDetailScreen groupID={42} />
      </QueryClientProvider>
    </AppRoot>,
  );
}

function authUser(id = 1) {
  useAuthStore.setState({
    status: 'authed',
    token: 'tok',
    user: {
      id,
      telegram_id: 42,
      username: 'durov',
      first_name: 'Pavel',
      tz: 'Europe/Moscow',
      dm_notify_default: true,
      is_superadmin: false,
    },
  });
}

beforeEach(() => {
  calls = [];
  handlers = {};
  configureAuth({ getToken: () => 'tok', getInitData: () => null, canReauth: () => true });
  authUser(1);
  stubFetch();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('GroupDetailScreen: шапка и привязка', () => {
  it('показывает слаг, статус, роль и счётчик участников', async () => {
    renderScreen();

    expect(await screen.findByTestId('group-header')).toBeTruthy();
    expect(screen.getByTestId('group-header').textContent).toContain('М8О-401Б-23');
    expect(screen.getByTestId('group-status').textContent).toContain('активна');
    expect(screen.getByTestId('group-status').textContent).toContain('админ');
    expect(screen.getByTestId('group-header').textContent).toContain('Участников: 2');
  });

  it('привязанный чат показан названием', async () => {
    renderScreen();

    expect((await screen.findByTestId('binding-status')).textContent).toContain('ИУ7 401Б');
  });

  it('без привязки — инструкция /bind_group со слагом группы', async () => {
    handlers.binding = null;
    renderScreen();

    const cell = await screen.findByTestId('binding-status');
    expect(cell.textContent).toContain('Чат не привязан');
    expect(cell.textContent).toContain('/bind_group М8О-401Б-23');
  });

  it('pending-группа показывает статус «ожидает привязки»', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push({ url, method: init?.method ?? 'GET', body: undefined });
        if (url === '/api/v1/groups/42') {
          return json(200, { group: { ...group, status: 'pending' }, role: 'member', binding: null, members_count: 1 });
        }
        return json(200, []);
      }),
    );
    renderScreen();

    expect((await screen.findByTestId('group-status')).textContent).toContain('ожидает привязки');
    expect(screen.getByTestId('binding-status').textContent).toContain('не привязан');
  });
});

describe('GroupDetailScreen: гейт по роли', () => {
  it('участник не видит админ-панели', async () => {
    handlers.role = 'member';
    renderScreen();

    await screen.findByTestId('group-status');
    // Участнику доступен только состав (без действий) и выход из группы.
    expect(screen.queryByTestId('invites-section')).toBeNull();
    expect(screen.queryByTestId('danger-section')).toBeNull();
    expect(screen.queryByTestId('moderation-section')).toBeNull();
    expect(screen.queryByTestId('open-invite')).toBeNull();
    expect(screen.getByTestId('leave-section')).toBeTruthy();
  });

  it('участник не видит меню действий над другими участниками', async () => {
    handlers.role = 'member';
    renderScreen();

    const cell = await screen.findByTestId('member-2');
    // Ячейка участника — не кнопка: меню открывать нечем.
    expect(within(cell).queryByTestId('member-menu-2')).toBeNull();
    expect(within(cell).queryByRole('button')).toBeNull();
  });

  it('участник не видит мигающей пустой секции состава', async () => {
    // Список участников грузится отдельным запросом: до его ответа секция
    // обязана показывать загрузку, а не «в группе никого нет».
    handlers.role = 'member';
    let releaseMembers: (() => void) | null = null;
    const membersGate = new Promise<void>((resolve) => {
      releaseMembers = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push({ url, method: init?.method ?? 'GET', body: undefined });
        if (url === '/api/v1/groups/42') {
          return json(200, {
            group,
            role: 'member',
            binding: { chat_id: -100, chat_title: 'ИУ7 401Б' },
            members_count: 2,
          });
        }
        if (url === '/api/v1/groups/42/members') {
          await membersGate;
          return json(200, members);
        }
        return json(200, []);
      }),
    );
    renderScreen();

    const section = await screen.findByTestId('members-section');
    // Пока запрос в полёте — загрузка, а не пустой состав.
    expect(section.querySelector('[role="status"]')).toBeTruthy();

    await act(async () => {
      releaseMembers?.();
      await Promise.resolve();
    });
    await waitFor(() => expect(screen.getByTestId('member-2')).toBeTruthy());
  });

  it('ошибка загрузки состава видна участнику, а не выглядит пустой группой', async () => {
    handlers.role = 'member';
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push({ url, method: init?.method ?? 'GET', body: undefined });
        if (url === '/api/v1/groups/42') {
          return json(200, {
            group,
            role: 'member',
            binding: null,
            members_count: 2,
          });
        }
        if (url === '/api/v1/groups/42/members') {
          return json(500, { error: { code: 'internal', message: 'Внутренняя ошибка' } });
        }
        return json(200, []);
      }),
    );
    renderScreen();

    const alert = await screen.findByTestId('members-error');
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toBe('Внутренняя ошибка');
  });

  it('админ видит админ-панель целиком', async () => {
    handlers.role = 'admin';
    renderScreen();

    expect(await screen.findByTestId('members-section')).toBeTruthy();
    expect(screen.getByTestId('invites-section')).toBeTruthy();
    expect(screen.queryByTestId('moderation-section')).toBeNull(); // пустая модерация не рендерится
    expect(screen.getByTestId('danger-section')).toBeTruthy();
  });

  it('участник не видит панели модерации', async () => {
    handlers.role = 'admin';
    renderScreen();

    await screen.findByTestId('members-section');
    expect(screen.queryByTestId('moderation-section')).toBeNull();
  });
});

describe('GroupDetailScreen: админские действия над участниками', () => {
  it('меню действий открывается и promote шлёт PATCH с ролью admin', async () => {
    renderScreen();

    const memberRow = await screen.findByTestId('member-2');
    // Действия открывает сам Cell (кнопка), а не обёртка строки.
    await act(async () => {
      fireEvent.click(within(memberRow).getByRole('button'));
    });
    const menu = await screen.findByTestId('member-menu-2');
    expect(menu).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('promote-2'));
      await Promise.resolve();
    });

    await waitFor(() => {
      const call = calls.find((c) => c.url === '/api/v1/groups/42/members/2' && c.method === 'PATCH');
      expect(call?.body).toEqual({ role: 'admin' });
    });
  });

  it('себе действий не предлагает (нет меню на своей строке)', async () => {
    renderScreen();

    const own = await screen.findByTestId('member-1');
    // Своя строка — не кнопка вовсе: меню открывать нечем.
    expect(within(own).queryByRole('button')).toBeNull();
    await act(async () => {
      fireEvent.click(own);
    });
    expect(screen.queryByTestId('member-menu-1')).toBeNull();
  });

  it('kick требует подтверждения и затем шлёт DELETE', async () => {
    renderScreen();

    const memberRow = await screen.findByTestId('member-2');
    // Действия открывает сам Cell (кнопка), а не обёртка строки.
    await act(async () => {
      fireEvent.click(within(memberRow).getByRole('button'));
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('kick-2'));
    });

    // Случайный тап не должен исключать участника.
    expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/v1/groups/42/members/2')).toBe(false);
    expect(screen.getByText('Исключить участника?')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-kick'));
      await Promise.resolve();
    });
    await waitFor(() =>
      expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/v1/groups/42/members/2')).toBe(true),
    );
  });

  it('409 «последний админ» показывается видимой ошибкой, а не молчанием', async () => {
    handlers.memberRole = [409, {}];
    renderScreen();

    const memberRow = await screen.findByTestId('member-2');
    await act(async () => {
      fireEvent.click(within(memberRow).getByRole('button'));
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('promote-2'));
      await Promise.resolve();
    });

    const alert = await screen.findByTestId('action-error');
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toContain('последний админ');
  });
});

describe('GroupDetailScreen: инвайты', () => {
  it('код показывается один раз и попадает в сессионный список', async () => {
    renderScreen();

    const open_invite_el = await screen.findByTestId('open-invite');
      await act(async () => {
        fireEvent.click(open_invite_el);
      });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-invite'));
      await Promise.resolve();
    });

    // Код виден в шите (единственный шанс его скопировать).
    expect(await screen.findByTestId('invite-code-value')).toBeTruthy();
    expect(screen.getByTestId('invite-code-value').textContent).toContain('ABCD2345');

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Закрыть'));
    });

    // После закрытия шита код остаётся в сессионном списке с отзывом.
    expect(await screen.findByTestId('session-invite-ABCD2345')).toBeTruthy();
    expect(screen.getByTestId('revoke-invite-ABCD2345')).toBeTruthy();
    const post = calls.find((c) => c.url === '/api/v1/groups/42/invites' && c.method === 'POST');
    expect(post?.body).toEqual({ role: 'member', max_uses: -1, ttl_hours: 168, publish_to_chat: false });
  });

  it('выбранная роль попадает и в запрос, и в подпись сессионного кода', async () => {
    renderScreen();

    const open_invite_el = await screen.findByTestId('open-invite');
    await act(async () => {
      fireEvent.click(open_invite_el);
    });
    // Админ выдаёт админский инвайт: подпись списка обязана это отражать,
    // иначе он решит, что код выдаёт участников.
    await act(async () => {
      fireEvent.change(screen.getByTestId('invite-role'), { target: { value: 'admin' } });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-invite'));
      await Promise.resolve();
    });
    await act(async () => {
      fireEvent.click(screen.getByLabelText('Закрыть'));
    });

    const post = calls.find((c) => c.url === '/api/v1/groups/42/invites' && c.method === 'POST');
    expect((post?.body as { role?: string })?.role).toBe('admin');
    const row = await screen.findByTestId('session-invite-ABCD2345');
    expect(row.textContent).toContain('админ при вступлении');
  });

  it('отзыв кода подтверждается и шлёт DELETE с этим кодом', async () => {
    renderScreen();

    const open_invite_el = await screen.findByTestId('open-invite');
      await act(async () => {
        fireEvent.click(open_invite_el);
      });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-invite'));
      await Promise.resolve();
    });
    await act(async () => {
      fireEvent.click(screen.getByLabelText('Закрыть'));
    });

    const revoke_invite_ABCD2345_el = await screen.findByTestId('revoke-invite-ABCD2345');
      await act(async () => {
        fireEvent.click(revoke_invite_ABCD2345_el);
      });
    expect(screen.getByText('Отозвать инвайт-код?')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-invite-revoke'));
      await Promise.resolve();
    });
    await waitFor(() =>
      expect(
        calls.some((c) => c.url === '/api/v1/groups/42/invites/ABCD2345' && c.method === 'DELETE'),
      ).toBe(true),
    );
    // Отозванный код исчезает из списка.
    await waitFor(() => expect(screen.queryByTestId('session-invite-ABCD2345')).toBeNull());
  });
});

describe('GroupDetailScreen: модерация дедлайнов', () => {
  it('заявка участника видна админу и одобрение шлёт POST /deadlines/{id}/approve', async () => {
    handlers.pendingDeadlines = [
      {
        id: 7,
        group_id: 42,
        owner_user_id: null,
        created_by: 2,
        title: 'Сдать курсовую',
        description: '',
        due_at: '2026-10-10T12:00:00Z',
        tz: 'Europe/Moscow',
        status: 'pending_approval',
        created_at: '2026-10-01T12:00:00Z',
        updated_at: '2026-10-01T12:00:00Z',
      },
    ];
    renderScreen();

    const row = await screen.findByTestId('pending-deadline-7');
    expect(row.textContent).toContain('Сдать курсовую');

    await act(async () => {
      fireEvent.click(screen.getByTestId('approve-deadline-7'));
    });
    expect(screen.getByText('Опубликовать дедлайн «Сдать курсовую»?')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-approve-deadline'));
      await Promise.resolve();
    });
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/deadlines/7/approve' && c.method === 'POST')).toBe(true),
    );
  });

  it('отклонение подтверждается и шлёт POST /deadlines/{id}/reject', async () => {
    handlers.pendingDeadlines = [
      {
        id: 7,
        group_id: 42,
        owner_user_id: null,
        created_by: 2,
        title: 'Сдать курсовую',
        description: '',
        due_at: '2026-10-10T12:00:00Z',
        tz: 'Europe/Moscow',
        status: 'pending_approval',
        created_at: '2026-10-01T12:00:00Z',
        updated_at: '2026-10-01T12:00:00Z',
      },
    ];
    renderScreen();

    await screen.findByTestId('pending-deadline-7');
    await act(async () => {
      fireEvent.click(screen.getByTestId('reject-deadline-7'));
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-reject-deadline'));
      await Promise.resolve();
    });
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/deadlines/7/reject' && c.method === 'POST')).toBe(true),
    );
  });

  it('без заявок секция модерации не рендерится', async () => {
    renderScreen();

    await screen.findByTestId('members-section');
    expect(screen.queryByTestId('moderation-section')).toBeNull();
  });
});

describe('GroupDetailScreen: выход и удаление', () => {
  it('выход подтверждается и шлёт DELETE /groups/{id}/me', async () => {
    handlers.role = 'member';
    renderScreen();

    const open_leave_el = await screen.findByTestId('open-leave');
      await act(async () => {
        fireEvent.click(open_leave_el);
      });
    expect(calls.some((c) => c.url === '/api/v1/groups/42/me')).toBe(false);

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-leave'));
      await Promise.resolve();
    });
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/groups/42/me' && c.method === 'DELETE')).toBe(true),
    );
  });

  it('участник не может удалить группу (нет опасной зоны)', async () => {
    handlers.role = 'member';
    renderScreen();

    await screen.findByTestId('leave-section');
    expect(screen.queryByTestId('open-delete-group')).toBeNull();
  });

  it('удаление группы админом подтверждается и шлёт DELETE /groups/{id}', async () => {
    handlers.role = 'admin';
    renderScreen();

    const open_delete_group_el = await screen.findByTestId('open-delete-group');
      await act(async () => {
        fireEvent.click(open_delete_group_el);
      });
    expect(screen.getByText('Удалить группу М8О-401Б-23?')).toBeTruthy();

    await act(async () => {
      fireEvent.click(screen.getByTestId('confirm-delete-group'));
      await Promise.resolve();
    });
    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/groups/42' && c.method === 'DELETE')).toBe(true),
    );
  });
});