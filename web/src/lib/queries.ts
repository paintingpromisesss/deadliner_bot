// Запросы TanStack Query для дедлайнов и групп (спека §9: «стейт — TanStack
// Query + zustand»). Здесь собраны ключи и хуки, чтобы экраны не дублировали
// инвалидацию и haptic-отклик мутаций.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  completeDeadline,
  createDeadline,
  deleteDeadline,
  fetchDeadline,
  fetchDeadlines,
  fetchMyGroups,
  updateDeadline,
  type Deadline,
  type DeadlineCreateInput,
  type DeadlinePatch,
  type GroupSummary,
} from './deadlines';
import {
  confirmClaim,
  createGroup,
  createInvite,
  deleteGroup,
  fetchGroupDetail,
  fetchGroups,
  fetchMembers,
  fetchNotificationSettings,
  kickMember,
  leaveGroup,
  patchNotificationSettings,
  redeemInvite,
  revokeClaim,
  revokeInvite,
  setMemberRole,
  startClaim,
  type InviteCreated,
  type InviteInput,
  type NotificationSettings,
} from './groups';
import { hapticNotification } from './tma';

/** Корневой ключ дедлайнов: инвалидация по префиксу покрывает все окна/фильтры. */
export const DEADLINES_KEY = 'deadlines';
export const GROUPS_KEY = 'groups';

/**
 * Ключ настроек уведомлений. Отдельная константа, потому что эффективное
 * значение группы зависит от ОБЩЕГО дефолта (spec §5.2: COALESCE(membership,
 * users.dm_notify_default)): смена дефолта обязана инвалидировать эти же
 * данные, иначе строки групп показывали бы устаревшее значение.
 */
export const NOTIFICATIONS_QUERY_KEY = [GROUPS_KEY, 'notifications'] as const;

/**
 * Ключ списка. Диапазон from/to — часть ключа: календарь и главный экран грузят
 * разные окна и не должны перетирать кэш друг друга.
 */
export function deadlinesQueryKey(params: { from?: string; to?: string } = {}) {
  return [DEADLINES_KEY, 'all', params.from ?? null, params.to ?? null] as const;
}

/** Все дедлайны пользователя (личные + групповые): список и hero-карточка. */
export function useAllDeadlines(params: { from?: string; to?: string } = {}) {
  return useQuery({
    queryKey: deadlinesQueryKey(params),
    queryFn: () => fetchDeadlines({ scope: 'all', from: params.from, to: params.to }),
    staleTime: 30_000,
    // Смена диапазона (листание месяца в календаре) не должна на миг оставлять
    // экран пустым: до прихода новых данных показываем предыдущие.
    placeholderData: keepPreviousData,
  });
}

/** Мои группы с ролями — селектор типа в форме и подписи групп в списках. */
export function useMyGroups() {
  return useQuery<GroupSummary[]>({
    queryKey: [GROUPS_KEY, 'mine'],
    queryFn: fetchMyGroups,
    staleTime: 300_000,
  });
}

/** GET /deadlines/{id} — детали с напоминаниями (режим правки формы). */
export function useDeadlineDetail(id: number | null) {
  return useQuery({
    queryKey: [DEADLINES_KEY, 'detail', id],
    queryFn: () => fetchDeadline(id as number),
    enabled: id !== null,
    staleTime: 10_000,
  });
}

/** id группы → её слаг; нужен для подписи «М8О-401Б-23» в списках дедлайнов. */
export function groupSlugMap(groups: GroupSummary[] | undefined): Map<number, string> {
  const map = new Map<number, string>();
  for (const item of groups ?? []) map.set(item.group.id, item.group.slug);
  return map;
}

/** Группы, в которых пользователь может писать групповые дедлайны (роль admin). */
export function adminGroups(groups: GroupSummary[] | undefined): GroupSummary[] {
  return (groups ?? []).filter((g) => g.role === 'admin' && g.group.status !== 'archived');
}

/** Инвалидация всех списков дедлайнов после любой мутации. */
function useInvalidateDeadlines() {
  const client = useQueryClient();
  return () => client.invalidateQueries({ queryKey: [DEADLINES_KEY] });
}

/** Создание: POST /deadlines + инвалидация списков. */
export function useCreateDeadline() {
  const invalidate = useInvalidateDeadlines();
  return useMutation({
    mutationFn: (input: DeadlineCreateInput) => createDeadline(input),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Правка: PATCH /deadlines/{id}; сервер сам перегенерирует напоминания. */
export function useUpdateDeadline() {
  const invalidate = useInvalidateDeadlines();
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: DeadlinePatch }) => updateDeadline(id, patch),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Удаление (soft delete). */
export function useDeleteDeadline() {
  const invalidate = useInvalidateDeadlines();
  return useMutation({
    mutationFn: (id: number) => deleteDeadline(id),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Отметка «выполнено» (спека §5.2: POST /deadlines/{id}/complete). */
export function useCompleteDeadline() {
  const invalidate = useInvalidateDeadlines();
  return useMutation({
    mutationFn: (id: number) => completeDeadline(id),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Утилита для тестов/компонентов: плоский список из ответа (страховка от null). */
export function asDeadlines(data: Deadline[] | undefined): Deadline[] {
  return data ?? [];
}

// --- Группы, участники, инвайты, claim и настройки уведомлений ---------------
//
// Ключи сгруппированы под GROUPS_KEY: любая мутация состава группы
// инвалидирует префикс и обновляет заодно список для селектора типов дедлайна
// и подписи групп (useMyGroups).

/** Детали одной группы с привязкой и счётчиком участников. */
export function useGroupDetail(id: number | null) {
  return useQuery({
    queryKey: [GROUPS_KEY, 'detail', id],
    queryFn: () => fetchGroupDetail(id as number),
    enabled: id !== null,
    staleTime: 10_000,
  });
}

/** Участники группы: список читают и админ-панель, и обычный участник
 * (состав виден всем членам группы — backend разрешает ListMembers любому
 * участнику, спека §5.2 обещает админский доступ). */
export function useGroupMembers(id: number | null, enabled = true) {
  return useQuery({
    queryKey: [GROUPS_KEY, 'members', id],
    queryFn: () => fetchMembers(id as number),
    enabled: id !== null && enabled,
    staleTime: 10_000,
  });
}

/**
 * Поиск/подсказка слага. Пустой запрос отключён: без q сервер отдаёт «мои
 * группы», и подсказки показывали бы уже вступленные группы как «вступить».
 */
export function useGroupSearch(q: string) {
  return useQuery<GroupSummary[]>({
    queryKey: [GROUPS_KEY, 'search', q],
    queryFn: () => fetchGroups(q),
    enabled: q.length > 0,
    staleTime: 30_000,
  });
}

/** Настройки ЛС-дублей: общий дефолт + переопределения по группам. */
export function useNotificationSettings() {
  return useQuery<NotificationSettings>({
    queryKey: NOTIFICATIONS_QUERY_KEY,
    queryFn: fetchNotificationSettings,
    staleTime: 60_000,
  });
}

/** Инвалидация всего группового префикса (состав, роли, детали, настройки). */
function useInvalidateGroups() {
  const client = useQueryClient();
  return () => client.invalidateQueries({ queryKey: [GROUPS_KEY] });
}

/** Создание группы: POST /groups → 201 {group}. */
export function useCreateGroup() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (input: { slug: string; title: string }) => createGroup(input),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Вступление по инвайт-коду: POST /invites/redeem → 200 {group}. */
export function useRedeemInvite() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (code: string) => redeemInvite(code),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Выход из группы: DELETE /groups/{id}/me. */
export function useLeaveGroup() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (groupID: number) => leaveGroup(groupID),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Удаление группы (soft delete): DELETE /groups/{id}, только admin. */
export function useDeleteGroup() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (groupID: number) => deleteGroup(groupID),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Роль участника: promote/demote (PATCH /groups/{id}/members/{user_id}). */
export function useSetMemberRole() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (vars: { groupID: number; userID: number; role: 'admin' | 'member' }) =>
      setMemberRole(vars.groupID, vars.userID, vars.role),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Исключение участника: DELETE /groups/{id}/members/{user_id}. */
export function useKickMember() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (vars: { groupID: number; userID: number }) =>
      kickMember(vars.groupID, vars.userID),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Создание инвайта: код возвращается один раз — кэшировать его нельзя. */
export function useCreateInvite() {
  return useMutation<InviteCreated, Error, { groupID: number; input: InviteInput }>({
    mutationFn: (vars) => createInvite(vars.groupID, vars.input),
    onSuccess: () => {
      hapticNotification('success');
    },
    onError: () => hapticNotification('error'),
  });
}

/** Отзыв инвайта по plaintext-коду. */
export function useRevokeInvite() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (vars: { groupID: number; code: string }) => revokeInvite(vars.groupID, vars.code),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Старт claim-флоу: бот постит код в привязанный чат. */
export function useStartClaim() {
  return useMutation({
    mutationFn: (groupID: number) => startClaim(groupID),
    // Успех отмечает отдельный шаг (подтверждение): haptic на «код отправлен»
    // дублировал бы отклик подтверждения — здесь только ошибка.
    onError: () => hapticNotification('error'),
  });
}

/** Подтверждение claim-кода: роль admin и (для pending) статус active. */
export function useConfirmClaim() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (vars: { groupID: number; code: string }) => confirmClaim(vars.groupID, vars.code),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/** Отзыв активного claim-кода (admin). */
export function useRevokeClaim() {
  const invalidate = useInvalidateGroups();
  return useMutation({
    mutationFn: (groupID: number) => revokeClaim(groupID),
    onSuccess: () => {
      hapticNotification('success');
      void invalidate();
    },
    onError: () => hapticNotification('error'),
  });
}

/**
 * Переключение ЛС-дублей: без group_id — общий дефолт, с group_id —
 * переопределение (dm_notify: null снимает его и возвращает наследование).
 * Ответ — актуальные настройки целиком, ими и обновляем кэш.
 */
export function usePatchNotificationSettings() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: { group_id?: number; dm_notify: boolean | null }) =>
      patchNotificationSettings(body),
    onSuccess: (settings) => {
      hapticNotification('success');
      client.setQueryData(NOTIFICATIONS_QUERY_KEY, settings);
      // Общий дефолт живёт ещё и в профиле (/me): держим стор в согласии.
      void client.invalidateQueries({ queryKey: [GROUPS_KEY] });
    },
    onError: () => hapticNotification('error'),
  });
}