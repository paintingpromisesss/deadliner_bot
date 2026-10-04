// Типизированный слой API групп, участников и инвайтов (спека §5.2). Поля —
// ровно те, что отдают контроллеры backend
// (internal/platform/httpapi/groups_controller.go).
import { apiFetch, type ApiError } from './api';
import type { GroupSummary } from './deadlines';

export type { GroupSummary };

/** Роль в группе: '' — не участник (так её отдаёт поиск). */
export type GroupRole = '' | 'admin' | 'member';

/** Группа в ответе POST /groups и GET /groups/{id} (DTO `group`). */
export interface Group {
  id: number;
  slug: string;
  title: string;
  status: 'pending' | 'active' | 'archived' | string;
  official: boolean;
  created_by: number;
  /** Минуты: 10080/4320/1440 (7д/3д/24ч). */
  default_presets: number[];
  /** TTL pending-группы (14 дней без привязки/админа — автоудаление). */
  claim_expires_at?: string | null;
  created_at: string;
  updated_at: string;
}

/** Привязка чата: null, если чат не привязан (спека §6.1, /bind_group). */
export interface Binding {
  chat_id: number;
  message_thread_id?: number | null;
  chat_title: string;
}

/** Ответ GET /groups/{id} → {group, role, binding, members_count}. */
export interface GroupDetail {
  group: Group;
  role: GroupRole;
  binding: Binding | null;
  members_count: number;
}

/** Участник из GET /groups/{id}/members (DTO memberDTO). */
export interface Member {
  user_id: number;
  username: string;
  first_name: string;
  role: 'admin' | 'member';
  joined_at: string;
}

/** Ответ POST /groups/{id}/invites → 201 {code, expires_at}; код — один раз. */
export interface InviteCreated {
  code: string;
  /** RFC3339, UTC; null — бессрочный инвайт (no expiration). */
  expires_at: string | null;
  /** true — приглашение опубликовано в чат группы. */
  published?: boolean;
}

/** Тело создания инвайта: max_uses -1 — без лимита, ttl_hours -1 — бессрочный. */
export interface InviteInput {
  role: 'admin' | 'member';
  /** -1 — без лимита; ≥1 — число использований. */
  max_uses: number;
  /** -1 — бессрочный (no expiration); ≥1 — часы; максимум 2160 (90 дней). */
  ttl_hours: number;
  /** true — бот опубликует приглашение в привязанный чат группы. */
  publish_to_chat?: boolean;
}

/** GET /groups (без q) → мои группы; GET /groups?q= → подсказки слагов. */
export function fetchGroups(q?: string): Promise<GroupSummary[]> {
  const query = q ? `?q=${encodeURIComponent(q)}` : '';
  return apiFetch<GroupSummary[]>(`/groups${query}`);
}

/** POST /groups {slug,title} → 201 {group}. Слаг нормализуется на сервере. */
export function createGroup(input: { slug: string; title: string }): Promise<{ group: Group }> {
  return apiFetch<{ group: Group }>('/groups', { method: 'POST', body: input });
}

/** GET /groups/{id} → {group, role, binding, members_count}. */
export function fetchGroupDetail(id: number): Promise<GroupDetail> {
  return apiFetch<GroupDetail>(`/groups/${id}`);
}

/** GET /groups/{id}/members → полный список (доступен участникам). */
export function fetchMembers(id: number): Promise<Member[]> {
  return apiFetch<Member[]>(`/groups/${id}/members`);
}

/** PATCH /groups/{id}/members/{user_id} {role} — promote/demote. */
export function setMemberRole(
  groupID: number,
  userID: number,
  role: 'admin' | 'member',
): Promise<{ user_id: number; role: string }> {
  return apiFetch<{ user_id: number; role: string }>(`/groups/${groupID}/members/${userID}`, {
    method: 'PATCH',
    body: { role },
  });
}

/** DELETE /groups/{id}/members/{user_id} → 204 (kick). */
export function kickMember(groupID: number, userID: number): Promise<void> {
  return apiFetch<void>(`/groups/${groupID}/members/${userID}`, { method: 'DELETE' });
}

/** DELETE /groups/{id}/me → 204 (выйти из группы). */
export function leaveGroup(groupID: number): Promise<void> {
  return apiFetch<void>(`/groups/${groupID}/me`, { method: 'DELETE' });
}

/** DELETE /groups/{id} → 204 (soft delete, только admin). */
export function deleteGroup(groupID: number): Promise<void> {
  return apiFetch<void>(`/groups/${groupID}`, { method: 'DELETE' });
}

/** POST /groups/{id}/invites → 201 {code, expires_at}; код показывается один раз. */
export function createInvite(groupID: number, input: InviteInput): Promise<InviteCreated> {
  return apiFetch<InviteCreated>(`/groups/${groupID}/invites`, { method: 'POST', body: input });
}

/** DELETE /groups/{id}/invites/{code} → 204 (отзыв по plaintext-коду). */
export function revokeInvite(groupID: number, code: string): Promise<void> {
  return apiFetch<void>(`/groups/${groupID}/invites/${encodeURIComponent(code)}`, {
    method: 'DELETE',
  });
}

/** POST /invites/redeem {code} → 200 {group}. */
export function redeemInvite(code: string): Promise<{ group: Group }> {
  return apiFetch<{ group: Group }>('/invites/redeem', { method: 'POST', body: { code } });
}

/** GET /invites/{code} → {group}: превью для экрана «Вступить в группу?».
 * Лимит использования не расходуется. */
export function fetchInvitePreview(code: string): Promise<{ group: Group }> {
  return apiFetch<{ group: Group }>(`/invites/${encodeURIComponent(code)}`);
}

/** Настройка одной группы: effective-значение + признак переопределения. */
export interface NotificationGroup {
  group_id: number;
  slug: string;
  title: string;
  /** Эффективное значение: override ? membership.dm_notify : users.dm_notify_default. */
  dm_notify: boolean;
  /** true — группа не наследует дефолт (в membership записано своё значение). */
  override: boolean;
}

/** GET/PATCH /notifications/settings — одна и та же форма тела ответа. */
export interface NotificationSettings {
  dm_notify_default: boolean;
  groups: NotificationGroup[];
}

/** GET /notifications/settings → настройки ЛС-дублей по группам. */
export function fetchNotificationSettings(): Promise<NotificationSettings> {
  return apiFetch<NotificationSettings>('/notifications/settings');
}

/**
 * PATCH /notifications/settings. `dm_notify: null` снимает переопределение
 * (группа снова наследует общий дефолт) — backend различает null и отсутствие
 * поля, поэтому здесь нужен явный null, а не пропуск ключа.
 */
export function patchNotificationSettings(body: {
  group_id?: number;
  dm_notify: boolean | null;
}): Promise<NotificationSettings> {
  return apiFetch<NotificationSettings>('/notifications/settings', {
    method: 'PATCH',
    body,
  });
}

/** Код ошибки API в виде строки ('' — ошибка не от API). */
export function errorCode(err: unknown): string {
  return typeof (err as Partial<ApiError> | null)?.code === 'string'
    ? (err as ApiError).code
    : '';
}

/** HTTP-статус ошибки API (0 — ошибка не от API). */
export function errorStatus(err: unknown): number {
  const status = (err as Partial<ApiError> | null)?.status;
  return typeof status === 'number' ? status : 0;
}

/** Задержка Retry-After из 429 (мс) либо null. */
export function errorRetryAfterMs(err: unknown): number | null {
  const ms = (err as Partial<ApiError> | null)?.retryAfterMs;
  return typeof ms === 'number' ? ms : null;
}
