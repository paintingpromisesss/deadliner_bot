// Экранный тест «Группы» (спека §9, экран 5): список моих групп с ролями,
// дебаунс поиска (один запрос на серию нажатий), подсказки слага, создание
// группы с клиентской валидацией и вступление по инвайт-коду.
//
// Дебаунс проверяется явным числом запросов: без него каждое нажатие клавиши
// уходило бы в GET /groups?q=…, а это полнотабличный LIKE на сервере.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot } from '../../components/ui';

import { GroupsScreen } from './GroupsScreen';
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

/** Группа в форме ответа GET /groups: {group, role}. */
function summary(id: number, slug: string, role: string, overrides: Record<string, unknown> = {}) {
  return {
    group: {
      id,
      slug,
      title: slug,
      status: 'active',
      official: false,
      created_by: 1,
      default_presets: [10080, 4320, 1440],
      created_at: '2026-09-01T00:00:00Z',
      updated_at: '2026-09-01T00:00:00Z',
      ...overrides,
    },
    role,
  };
}

interface Handlers {
  mine?: unknown[];
  search?: unknown[];
  createStatus?: number;
  createBody?: unknown;
  redeemStatus?: number;
  redeemCode?: string;
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

      if (url.startsWith('/api/v1/groups?')) {
        return json(200, handlers.search ?? []);
      }
      if (url === '/api/v1/groups' && method === 'GET') {
        return json(200, handlers.mine ?? []);
      }
      if (url === '/api/v1/groups' && method === 'POST') {
        const status = handlers.createStatus ?? 201;
        if (status >= 400) {
          return json(status, {
            error: {
              code: status === 409 ? 'conflict' : status === 429 ? 'rate_limit' : 'slug_invalid',
              message: 'ошибка сервера',
            },
          });
        }
        return json(201, { group: { ...summary(7, 'М8О-401Б-23', '').group, ...(handlers.createBody ?? {}) } });
      }
      if (url === '/api/v1/invites/redeem') {
        const status = handlers.redeemStatus ?? 200;
        if (status >= 400) {
          const code = handlers.redeemCode ?? (status === 409 ? 'conflict' : 'not_found');
          return json(status, { error: { code, message: 'Сообщение сервера' } });
        }
        return json(200, { group: summary(11, 'ИКБО-33-21', '').group });
      }
      if (url.startsWith('/api/v1/groups/')) {
        return json(200, { group: summary(7, 'М8О-401Б-23', 'admin').group, role: 'admin', binding: null, members_count: 1 });
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
        <GroupsScreen />
      </QueryClientProvider>
    </AppRoot>,
  );
}

/** Запросы поиска (GET /groups?q=…). */
function searchCalls(): Call[] {
  return calls.filter((c) => c.url.startsWith('/api/v1/groups?'));
}

beforeEach(() => {
  calls = [];
  handlers = {};
  configureAuth({ getToken: () => 'tok', getInitData: () => null, canReauth: () => true });
  useAuthStore.setState({
    status: 'authed',
    token: 'tok',
    user: {
      id: 1,
      telegram_id: 42,
      username: 'durov',
      first_name: 'Pavel',
      tz: 'Europe/Moscow',
      dm_notify_default: true,
      is_superadmin: false,
    },
  });
  stubFetch();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('GroupsScreen: список моих групп', () => {
  it('показывает группы с ролью в подписи и бейджем', async () => {
    handlers.mine = [summary(42, 'М8О-401Б-23', 'admin'), summary(43, 'ИКБО-33-21', 'member')];
    renderScreen();

    expect(await screen.findByTestId('group-42')).toBeTruthy();
    expect(screen.getByTestId('group-43')).toBeTruthy();
    expect(screen.getByTestId('group-42').textContent).toContain('М8О-401Б-23');
    expect(screen.getByTestId('group-42').textContent).toContain('админ');
    expect(screen.getByTestId('group-43').textContent).toContain('участник');
  });

  it('pending-группа помечена статусом, активная — без лишней пометки', async () => {
    handlers.mine = [
      summary(42, 'М8О-401Б-23', 'member', { status: 'pending' }),
      summary(43, 'ИКБО-33-21', 'member'),
    ];
    renderScreen();

    await screen.findByTestId('group-42');
    expect(screen.getByTestId('group-42').textContent).toContain('ожидает привязки');
    expect(screen.getByTestId('group-43').textContent).not.toContain('ожидает привязки');
  });

  it('пустой список — плейсхолдер с призывом создать группу', async () => {
    handlers.mine = [];
    renderScreen();

    expect(await screen.findByText('Вы не состоите ни в одной группе')).toBeTruthy();
    expect(screen.getByTestId('empty-create')).toBeTruthy();
  });

  it('ошибка загрузки списка — плейсхолдер и повтор', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json(500, { error: { code: 'internal', message: 'Внутренняя ошибка' } })),
    );
    renderScreen();

    expect(await screen.findByText('Не удалось загрузить данные')).toBeTruthy();
  });
});

describe('GroupsScreen: поиск с дебаунсом', () => {
  // Дебаунс доказываем числом запросов, а не отсутствием запроса в первые
  // миллисекунды: без дебаунса четыре нажатия дали бы четыре GET /groups?q=…
  // (полнотабличный LIKE на сервере), с дебаунсом — ровно один, с финальным
  // запросом. Это утверждение не зависит от скорости машины.
  it('серия нажатий даёт ровно один запрос поиска — с финальным значением', async () => {
    handlers.mine = [];
    handlers.search = [summary(100, 'М8О-401Б-23', '')];
    renderScreen();

    const field = (await screen.findByTestId('field-search')) as HTMLInputElement;
    await act(async () => {
      for (const value of ['М', 'М8', 'М8О', 'М8О-']) {
        fireEvent.change(field, { target: { value } });
      }
    });

    await screen.findByTestId('suggestion-100');
    expect(searchCalls()).toHaveLength(1);
    expect(searchCalls()[0].url).toBe(`/api/v1/groups?q=${encodeURIComponent('М8О-')}`);
  });

  it('подсказка показывает найденную группу и поясняет, что вход — по инвайту', async () => {
    handlers.mine = [];
    handlers.search = [summary(100, 'М8О-401Б-23', '')];
    renderScreen();

    const field = (await screen.findByTestId('field-search')) as HTMLInputElement;
    await act(async () => {
      fireEvent.change(field, { target: { value: 'М8О' } });
    });

    expect(await screen.findByTestId('suggestion-100')).toBeTruthy();
    expect(screen.getByTestId('suggestion-100').textContent).toContain('М8О-401Б-23');
    expect(screen.getByTestId('suggestion-100').textContent).toContain(
      'Вступить в группу можно по инвайт-коду',
    );
  });

  it('пустой результат поиска — подсказка «ничего не найдено»', async () => {
    handlers.mine = [];
    handlers.search = [];
    renderScreen();

    const field = (await screen.findByTestId('field-search')) as HTMLInputElement;
    await act(async () => {
      fireEvent.change(field, { target: { value: 'неттакой' } });
    });

    expect(await screen.findByTestId('search-empty')).toBeTruthy();
  });

  it('уже вступленные группы не дублируются в подсказках', async () => {
    handlers.mine = [summary(42, 'М8О-401Б-23', 'admin')];
    handlers.search = [summary(42, 'М8О-401Б-23', 'admin'), summary(100, 'М8О-402Б-23', '')];
    renderScreen();

    await screen.findByTestId('group-42');
    const field = screen.getByTestId('field-search') as HTMLInputElement;
    await act(async () => {
      fireEvent.change(field, { target: { value: 'М8О' } });
    });

    expect(await screen.findByTestId('suggestion-100')).toBeTruthy();
    expect(screen.queryByTestId('suggestion-42')).toBeNull();
  });

  it('совпадения только среди моих групп — подсказка «уже в ваших», а не «не найдено»', async () => {
    // Искомое существует и видно пользователю прямо выше: сказать «ничего не
    // найдено» значило бы противоречить собственному списку на экране.
    handlers.mine = [summary(42, 'М8О-401Б-23', 'admin')];
    handlers.search = [summary(42, 'М8О-401Б-23', 'admin')];
    renderScreen();

    await screen.findByTestId('group-42');
    const field = screen.getByTestId('field-search') as HTMLInputElement;
    await act(async () => {
      fireEvent.change(field, { target: { value: 'М8О' } });
    });

    expect(await screen.findByTestId('search-all-mine')).toBeTruthy();
    expect(screen.queryByTestId('search-empty')).toBeNull();
    expect(screen.queryByTestId('suggestion-42')).toBeNull();
  });

  it('без введённого текста поиск не запрашивается', async () => {
    handlers.mine = [summary(42, 'М8О-401Б-23', 'admin')];
    renderScreen();

    await screen.findByTestId('group-42');
    expect(searchCalls()).toHaveLength(0);
  });
});

describe('GroupsScreen: создание группы', () => {
  it('ошибочный слаг не уходит в сеть, а объясняется на месте', async () => {
    handlers.mine = [];
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-create'));
    });
    const slug = (await screen.findByTestId('field-slug')) as HTMLInputElement;
    const title = screen.getByTestId('field-group-title') as HTMLInputElement;

    await act(async () => {
      fireEvent.change(slug, { target: { value: 'asdf' } }); // без цифры
      fireEvent.change(title, { target: { value: 'Группа' } });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-create-group'));
      await Promise.resolve();
    });

    expect(await screen.findByTestId('error-slug')).toBeTruthy();
    expect(screen.getByTestId('error-slug').textContent).toBe(
      'Слаг должен содержать хотя бы одну цифру',
    );
    // Ни одного POST /groups: отказ клиента не тратит серверный лимит.
    expect(calls.some((c) => c.url === '/api/v1/groups' && c.method === 'POST')).toBe(false);
  });

  it('валидный слаг нормализуется перед отправкой', async () => {
    handlers.mine = [];
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-create'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-slug'), {
        target: { value: ' м8о-401б-23 ' },
      });
      fireEvent.change(screen.getByTestId('field-group-title'), {
        target: { value: 'М8О-401Б-23 (ИУ7)' },
      });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-create-group'));
      await Promise.resolve();
    });

    await waitFor(() =>
      expect(calls.some((c) => c.url === '/api/v1/groups' && c.method === 'POST')).toBe(true),
    );
    const post = calls.find((c) => c.url === '/api/v1/groups' && c.method === 'POST');
    expect(post?.body).toEqual({ slug: 'М8О-401Б-23', title: 'М8О-401Б-23 (ИУ7)' });
  });

  it('409 (номер занят) показывается строкой в шите', async () => {
    handlers.mine = [];
    handlers.createStatus = 409;
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-create'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-slug'), { target: { value: 'М8О-401Б-23' } });
      fireEvent.change(screen.getByTestId('field-group-title'), { target: { value: 'Группа' } });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-create-group'));
      await Promise.resolve();
    });

    const alert = await screen.findByTestId('create-error');
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toContain('уже занят');
  });

  it('409 при создании группы по-прежнему означает занятый номер', async () => {
    // Контроль к предыдущему тесту: разведённые мапперы не должны были
    // «починить» redeem ценой поломки создания группы.
    handlers.mine = [];
    handlers.createStatus = 409;
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-create'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-slug'), { target: { value: 'М8О-401Б-23' } });
      fireEvent.change(screen.getByTestId('field-group-title'), { target: { value: 'Группа' } });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-create-group'));
      await Promise.resolve();
    });

    expect((await screen.findByTestId('create-error')).textContent).toContain('уже занят');
  });

  it('429 показывает задержку из Retry-After', async () => {
    handlers.mine = [];
    handlers.createStatus = 429;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? 'GET';
        calls.push({ url, method, body: undefined });
        if (url === '/api/v1/groups' && method === 'POST') {
          return new Response(
            JSON.stringify({ error: { code: 'rate_limit', message: 'Слишком часто' } }),
            { status: 429, headers: { 'Content-Type': 'application/json', 'Retry-After': '3600' } },
          );
        }
        return json(200, []);
      }),
    );
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-create'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-slug'), { target: { value: 'М8О-401Б-23' } });
      fireEvent.change(screen.getByTestId('field-group-title'), { target: { value: 'Группа' } });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-create-group'));
      await Promise.resolve();
    });

    const alert = await screen.findByTestId('create-error');
    expect(alert.textContent).toContain('Повторить через');
    expect(alert.textContent).toContain('1 час');
  });
});

describe('GroupsScreen: инвайт-код', () => {
  it('успешное вступление показывает уведомление и обновляет список', async () => {
    handlers.mine = [summary(42, 'М8О-401Б-23', 'admin')];
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-redeem'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-invite-code'), {
        target: { value: ' abcd2345 ' },
      });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-redeem'));
      await Promise.resolve();
    });

    const post = await waitFor(() => {
      const call = calls.find((c) => c.url === '/api/v1/invites/redeem');
      expect(call).toBeTruthy();
      return call as Call;
    });
    // Код нормализуется в верхний регистр: сервер ждёт plaintext без пробелов.
    expect(post.body).toEqual({ code: 'ABCD2345' });
    expect(await screen.findByTestId('groups-notice')).toBeTruthy();
  });

  it('409 (инвайт исчерпан) показывает текст про инвайт, а не про занятый слаг', async () => {
    // POST /invites/redeem отдаёт 409 при исчерпанном max_uses (IncrementUsed →
    // ErrConflict). Копирайт создания группы («номер уже занят») здесь был бы
    // грубой ошибкой: пользователь пошёл бы искать группу по номеру.
    handlers.mine = [];
    handlers.redeemStatus = 409;
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-redeem'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-invite-code'), {
        target: { value: 'ABCD2345' },
      });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-redeem'));
      await Promise.resolve();
    });

    const alert = await screen.findByTestId('redeem-error');
    expect(alert.textContent).toBe('Инвайт-код исчерпан или отозван.');
    expect(alert.textContent).not.toContain('занят');
  });

  it('404 (код неизвестен/отозван/истёк) даёт текст про код', async () => {
    handlers.mine = [];
    handlers.redeemStatus = 404;
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-redeem'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-invite-code'), {
        target: { value: 'ZZZZZZZZ' },
      });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-redeem'));
      await Promise.resolve();
    });

    expect((await screen.findByTestId('redeem-error')).textContent).toBe(
      'Код не найден, отозван или истёк',
    );
  });

  it('неизвестный код показывается строкой ошибки', async () => {
    handlers.mine = [];
    handlers.redeemStatus = 404;
    renderScreen();

    await act(async () => {
      fireEvent.click(await screen.findByTestId('open-redeem'));
    });
    await act(async () => {
      fireEvent.change(await screen.findByTestId('field-invite-code'), {
        target: { value: 'ZZZZZZZZ' },
      });
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('submit-redeem'));
      await Promise.resolve();
    });

    const alert = await screen.findByTestId('redeem-error');
    expect(alert.getAttribute('role')).toBe('alert');
    expect(alert.textContent).toBe('Код не найден, отозван или истёк');
  });
});