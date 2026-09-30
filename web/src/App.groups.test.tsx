// Интеграция маршрутов групп в App (спека §9): «#/groups» — список,
// «#/groups/{id}» — детали, мусорный id — список (а не пустой экран).
//
// Эти переходы не покрыть тестом отдельного экрана: решение принимает
// переключатель маршрутов, и ошибка в нём даёт либо вечный список, либо
// экран «Группа» без данных.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
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

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/v1/me') return json(200, user);
      if (url === '/api/v1/groups') return json(200, [{ group, role: 'admin' }]);
      if (url === '/api/v1/groups/42') {
        return json(200, { group, role: 'admin', binding: null, members_count: 1 });
      }
      if (url === '/api/v1/groups/42/members') {
        return json(200, [
          { user_id: 1, username: 'durov', first_name: 'Pavel', role: 'admin', joined_at: '2026-09-01T00:00:00Z' },
        ]);
      }
      if (url.startsWith('/api/v1/me/deadlines')) return json(200, []);
      return json(200, []);
    }),
  );
}

/** Прокручивает очередь задач, пока условие не выполнится (TanStack Query
 * резолвит запросы через микрозадачи + таймеры, одного flush мало). */
async function settle(check: () => boolean, attempts = 50): Promise<void> {
  for (let i = 0; i < attempts && !check(); i += 1) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  }
}

/** Монтирует App на заданном хеше и дожидается отрисовки. */
async function mountAt(hash: string): Promise<void> {
  window.localStorage.setItem(TOKEN_STORAGE_KEY, 'stored-token');
  window.location.hash = hash;
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
  await act(async () => {
    root?.render(<App />);
  });
}

beforeEach(() => {
  stubFetch();
});

afterEach(() => {
  if (root) {
    act(() => root?.unmount());
    root = null;
  }
  container?.remove();
  container = null;
  window.localStorage.clear();
  window.location.hash = '';
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('App: маршруты групп', () => {
  it('#/groups показывает список моих групп', async () => {
    await mountAt('#/groups');
    const el = () => container as HTMLElement;
    await settle(() => el().querySelector('[data-testid="group-42"]') !== null);

    expect(el().textContent).toContain('М8О-401Б-23');
    expect((container as HTMLElement).querySelector('[data-testid="group-42"]')).toBeTruthy();
    // Детали не отрисованы: это список.
    expect((container as HTMLElement).querySelector('[data-testid="group-header"]')).toBeNull();
  });

  it('#/groups/42 открывает детали группы', async () => {
    await mountAt('#/groups/42');
    const el = () => container as HTMLElement;
    await settle(() => el().querySelector('[data-testid="group-header"]') !== null);

    expect(el().querySelector('[data-testid="group-header"]')).toBeTruthy();
    expect((container as HTMLElement).querySelector('[data-testid="members-section"]')).toBeTruthy();
  });

  it('мусорный id не роняет экран: показывается список групп', async () => {
    await mountAt('#/groups/abc');
    const el = () => container as HTMLElement;
    await settle(() => el().querySelector('[data-testid="group-42"]') !== null);

    expect(el().querySelector('[data-testid="group-42"]')).toBeTruthy();
    expect((container as HTMLElement).querySelector('[data-testid="group-header"]')).toBeNull();
  });

  it('таб «Группы» остаётся активным и на деталях группы', async () => {
    await mountAt('#/groups/42');
    await settle(() => (container as HTMLElement).querySelector('[data-testid="group-header"]') !== null);
    const active = (container as HTMLElement).querySelector('[aria-current="page"]');
    expect(active?.textContent).toContain('Группы');
  });
});