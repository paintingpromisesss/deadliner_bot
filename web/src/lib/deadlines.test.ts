// Тесты типизированного слоя API дедлайнов: сборка query-строк и пути
// эндпоинтов (спека §5.2). Тела ответов мокаются на уровне fetch, поэтому
// проверяется и путь apiFetch (заголовок Bearer, разбор конверта ошибок).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, configureAuth } from './api';
import {
  buildListQuery,
  completeDeadline,
  createDeadline,
  deleteDeadline,
  fetchDeadlines,
  fetchGroupDeadlines,
  updateDeadline,
} from './deadlines';

const MSK = 'Europe/Moscow';

interface Call {
  url: string;
  method: string;
  auth: string | null;
  body: unknown;
}

let calls: Call[] = [];

function stubFetch(response: () => Response) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({
        url: String(input),
        method: init?.method ?? 'GET',
        auth: new Headers(init?.headers).get('Authorization'),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      });
      return response();
    }),
  );
}

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const deadline = {
  id: 5,
  group_id: 42,
  title: 'Курсовая',
  description: '',
  due_at: '2026-09-29T20:59:00Z',
  tz: MSK,
  status: 'active',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
};

beforeEach(() => {
  calls = [];
  configureAuth({ getToken: () => 'tok', getInitData: () => null, canReauth: () => true });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('buildListQuery', () => {
  it('пустые параметры не попадают в строку', () => {
    expect(buildListQuery()).toBe('');
    expect(buildListQuery({})).toBe('');
    expect(buildListQuery({ from: '', to: '', status: undefined })).toBe('');
  });

  it('собирает все параметры', () => {
    const q = buildListQuery({
      from: '2026-09-01T00:00:00Z',
      to: '2026-09-30T23:59:59Z',
      status: 'active',
      scope: 'all',
    });
    expect(q).toContain('from=2026-09-01T00%3A00%3A00Z');
    expect(q).toContain('to=2026-09-30T23%3A59%3A59Z');
    expect(q).toContain('status=active');
    expect(q).toContain('scope=all');
  });
});

describe('эндпоинты дедлайнов', () => {
  it('fetchDeadlines: GET /me/deadlines?scope=all с Bearer-токеном', async () => {
    stubFetch(() => json(200, [deadline]));
    const list = await fetchDeadlines({ scope: 'all', status: 'active' });
    expect(list[0].id).toBe(5);
    expect(calls[0].url).toBe('/api/v1/me/deadlines?status=active&scope=all');
    expect(calls[0].auth).toBe('Bearer tok');
  });

  it('fetchGroupDeadlines: GET /groups/{id}/deadlines', async () => {
    stubFetch(() => json(200, []));
    await fetchGroupDeadlines(42, { from: '2026-09-01T00:00:00Z' });
    expect(calls[0].url).toBe('/api/v1/groups/42/deadlines?from=2026-09-01T00%3A00%3A00Z');
  });

  it('createDeadline: POST /deadlines с телом в snake_case', async () => {
    stubFetch(() => json(201, { deadline, reminders: [{ id: 1, kind: 'preset', offset_minutes: 1440, fire_at: '2026-09-28T20:59:00Z', status: 'pending' }] }));
    const view = await createDeadline({
      group_id: 42,
      title: 'Курсовая',
      description: 'детали',
      due_at: '2026-09-29T20:59:00Z',
      tz: MSK,
      reminders: [{ kind: 'preset', offset_minutes: 1440 }],
    });
    expect(calls[0].method).toBe('POST');
    expect(calls[0].url).toBe('/api/v1/deadlines');
    expect(calls[0].body).toEqual({
      group_id: 42,
      title: 'Курсовая',
      description: 'детали',
      due_at: '2026-09-29T20:59:00Z',
      tz: MSK,
      reminders: [{ kind: 'preset', offset_minutes: 1440 }],
    });
    expect(view.reminders[0].offset_minutes).toBe(1440);
  });

  it('updateDeadline: PATCH /deadlines/{id}', async () => {
    stubFetch(() => json(200, { deadline, reminders: [] }));
    await updateDeadline(5, { title: 'Новое', due_at: '2026-10-01T20:59:00Z', tz: MSK });
    expect(calls[0].method).toBe('PATCH');
    expect(calls[0].url).toBe('/api/v1/deadlines/5');
    expect(calls[0].body).toEqual({ title: 'Новое', due_at: '2026-10-01T20:59:00Z', tz: MSK });
  });

  it('deleteDeadline: DELETE и пустой ответ 204', async () => {
    stubFetch(() => json(204, null));
    await expect(deleteDeadline(5)).resolves.toBeUndefined();
    expect(calls[0].method).toBe('DELETE');
    expect(calls[0].url).toBe('/api/v1/deadlines/5');
  });

  it('completeDeadline: POST /deadlines/{id}/complete', async () => {
    stubFetch(() => json(200, { deadline: { ...deadline, status: 'done' }, reminders: [] }));
    const view = await completeDeadline(5);
    expect(calls[0].method).toBe('POST');
    expect(calls[0].url).toBe('/api/v1/deadlines/5/complete');
    expect(view.deadline.status).toBe('done');
  });

  it('ошибка сервера превращается в ApiError с кодом и сообщением', async () => {
    stubFetch(() => json(403, { error: { code: 'forbidden', message: 'Недостаточно прав' } }));
    const err = await createDeadline({ title: 'X', due_at: '2026-09-29T20:59:00Z' }).catch(
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(403);
    expect((err as ApiError).code).toBe('forbidden');
    expect((err as ApiError).message).toBe('Недостаточно прав');
  });
});