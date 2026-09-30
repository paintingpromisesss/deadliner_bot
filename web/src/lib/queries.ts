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
import { hapticNotification } from './tma';

/** Корневой ключ дедлайнов: инвалидация по префиксу покрывает все окна/фильтры. */
export const DEADLINES_KEY = 'deadlines';
export const GROUPS_KEY = 'groups';

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