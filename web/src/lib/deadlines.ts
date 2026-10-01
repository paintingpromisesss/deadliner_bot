// Типизированный слой API дедлайнов (спека §5.2). Поля — ровно те, что отдаёт
// backend (internal/platform/httpapi/deadlines_controller.go): snake_case,
// времена — RFC3339 (в ответах всегда UTC).
import { apiFetch } from './api';

/** Дедлайн — DTO из списков (`GET /me/deadlines`, `GET /groups/{id}/deadlines`). */
export interface Deadline {
  id: number;
  /** null — персональный дедлайн. */
  group_id?: number | null;
  owner_user_id?: number | null;
  /**
   * Автор дедлайна (users.id). Права записи на групповой дедлайн — у автора и
   * админа группы (спека §5.2 «автор/admin»): по этому полю экран решает,
   * показывать ли «выполнить»/«удалить»/«сохранить».
   */
  created_by: number;
  title: string;
  description: string;
  /** RFC3339, UTC. */
  due_at: string;
  /** tz автора для отображения (спека §4). */
  tz: string;
  status: DeadlineStatus;
  created_at: string;
  updated_at: string;
}

export type DeadlineStatus = 'active' | 'done' | 'archived';

/** Тип напоминания (domain.ReminderKind). */
export type ReminderKind = 'preset' | 'custom_offset' | 'custom_at' | 'dm_dup';

export type ReminderStatus = 'pending' | 'sent' | 'failed' | 'cancelled';

/** Напоминание — элемент `reminders[]` в ответах single-deadline операций. */
export interface Reminder {
  id: number;
  kind: ReminderKind;
  offset_minutes?: number | null;
  fire_at: string;
  status: ReminderStatus;
  sent_at?: string | null;
}

/** Ответ {deadline, reminders} от POST/PATCH/GET /deadlines/{id}. */
export interface DeadlineView {
  deadline: Deadline;
  reminders: Reminder[];
}

/** Напоминание в теле запроса: kind + offset_minutes или fire_at. */
export interface ReminderInput {
  kind: Exclude<ReminderKind, 'dm_dup'>;
  offset_minutes?: number;
  /** RFC3339 — только для kind='custom_at'. */
  fire_at?: string;
}

export interface DeadlineCreateInput {
  /** null/undefined — персональный; иначе групповой (нужна роль admin). */
  group_id?: number | null;
  title: string;
  description?: string;
  /** RFC3339. */
  due_at: string;
  /** tz автора; пусто — сервер берёт tz пользователя. */
  tz?: string;
  reminders?: ReminderInput[];
}

/** PATCH: переданные поля меняются, отсутствующие не трогаются. */
export interface DeadlinePatch {
  title?: string;
  description?: string;
  due_at?: string;
  tz?: string;
}

export interface ListParams {
  /** RFC3339 — нижняя граница due_at (включительно). */
  from?: string;
  /** RFC3339 — верхняя граница due_at (включительно). */
  to?: string;
  status?: DeadlineStatus;
  /** 'all' — добавить групповые дедлайны из моих membership'ов. */
  scope?: 'all';
  signal?: AbortSignal;
}

/** Сборка query-строки: пустые значения не попадают в URL вообще. */
export function buildListQuery(params: ListParams = {}): string {
  const q = new URLSearchParams();
  if (params.from) q.set('from', params.from);
  if (params.to) q.set('to', params.to);
  if (params.status) q.set('status', params.status);
  if (params.scope) q.set('scope', params.scope);
  const s = q.toString();
  return s ? `?${s}` : '';
}

/** GET /me/deadlines?from&to&status&scope=all → [deadline]. */
export function fetchDeadlines(params: ListParams = {}): Promise<Deadline[]> {
  return apiFetch<Deadline[]>(`/me/deadlines${buildListQuery(params)}`, { signal: params.signal });
}

/** GET /deadlines/{id} → {deadline, reminders}. */
export function fetchDeadline(id: number): Promise<DeadlineView> {
  return apiFetch<DeadlineView>(`/deadlines/${id}`);
}

/** POST /deadlines → 201 {deadline, reminders}. */
export function createDeadline(input: DeadlineCreateInput): Promise<DeadlineView> {
  return apiFetch<DeadlineView>('/deadlines', { method: 'POST', body: input });
}

/** PATCH /deadlines/{id} → {deadline, reminders}; смена due_at перегенерирует напоминания. */
export function updateDeadline(id: number, patch: DeadlinePatch): Promise<DeadlineView> {
  return apiFetch<DeadlineView>(`/deadlines/${id}`, { method: 'PATCH', body: patch });
}

/** DELETE /deadlines/{id} → 204 (soft delete). */
export function deleteDeadline(id: number): Promise<void> {
  return apiFetch<void>(`/deadlines/${id}`, { method: 'DELETE' });
}

/** POST /deadlines/{id}/complete → {deadline, reminders}; статус done. */
export function completeDeadline(id: number): Promise<DeadlineView> {
  return apiFetch<DeadlineView>(`/deadlines/${id}/complete`, { method: 'POST' });
}

/** Группа в ответе GET /groups ({group, role}) — для селектора типа дедлайна. */
export interface GroupSummary {
  group: {
    id: number;
    slug: string;
    title: string;
    status: string;
    official: boolean;
    created_by: number;
    /** Минуты: 10080/4320/1440 (7д/3д/24ч). */
    default_presets: number[];
    claim_expires_at?: string | null;
    created_at: string;
    updated_at: string;
  };
  /** '' — не участник (в подсказках поиска). */
  role: '' | 'admin' | 'member';
}

/** GET /groups (без q) → [{group, role}] — мои группы. */
export function fetchMyGroups(): Promise<GroupSummary[]> {
  return apiFetch<GroupSummary[]>('/groups');
}