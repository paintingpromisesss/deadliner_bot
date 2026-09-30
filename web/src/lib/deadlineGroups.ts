// Чистая логика группировки дедлайнов: фильтр по типу (личные/групповые) и
// сегменты по сроку (Просрочено / Сегодня / 7 дней / Позже). Вынесено из
// экрана, чтобы проверяться тестом без React и без сети.
//
// Границы суток считаются в tz пользователя (спека §5), «просрочено» — строго
// раньше now. Выполненные (done) и архивные дедлайны в списки не попадают.
import { localDayNumber } from './format';
import type { Deadline } from './deadlines';

/** Фильтр по типу дедлайна (чипы «Все/Личные/Групповые»). */
export type ScopeFilter = 'all' | 'personal' | 'group';

export const SCOPE_FILTERS: readonly ScopeFilter[] = ['all', 'personal', 'group'];

/** Сегменты списка в порядке отображения. */
export type Segment = 'overdue' | 'today' | 'week' | 'later';

export const SEGMENTS: readonly Segment[] = ['overdue', 'today', 'week', 'later'];

export interface SegmentedDeadlines {
  overdue: Deadline[];
  today: Deadline[];
  week: Deadline[];
  later: Deadline[];
}

/** Дедлайн групповой, если у него есть group_id (спека §4: XOR владельца). */
export function isGroupDeadline(d: Deadline): boolean {
  return d.group_id !== null && d.group_id !== undefined;
}

export function isPersonalDeadline(d: Deadline): boolean {
  return !isGroupDeadline(d);
}

/** Фильтр по типу. Неизвестное значение трактуется как «all». */
export function filterByScope(list: readonly Deadline[], scope: ScopeFilter): Deadline[] {
  switch (scope) {
    case 'personal':
      return list.filter(isPersonalDeadline);
    case 'group':
      return list.filter(isGroupDeadline);
    default:
      return [...list];
  }
}

/** Только активные: done/archived в TMA-списках не показываются. */
export function onlyActive(list: readonly Deadline[]): Deadline[] {
  return list.filter((d) => d.status === 'active');
}

/**
 * Раскладка по сегментам. Деление — по календарным суткам в tz пользователя:
 *  - overdue: срок раньше now (в том числе «сегодня, но 5 минут назад»);
 *  - today:   те же сутки, что now, и срок ещё не наступил;
 *  - week:    следующие 6 суток (завтра..+6);
 *  - later:   всё, что дальше.
 * Внутри сегментов порядок — по возрастанию due_at.
 */
export function segmentize(
  list: readonly Deadline[],
  tz: string,
  now: Date | number,
): SegmentedDeadlines {
  const nowMs = now instanceof Date ? now.getTime() : now;
  const nowDay = localDayNumber(nowMs, tz);

  const out: SegmentedDeadlines = { overdue: [], today: [], week: [], later: [] };

  for (const d of list) {
    if (d.status !== 'active') continue;
    const dueMs = Date.parse(d.due_at);
    if (Number.isNaN(dueMs)) continue;

    if (dueMs < nowMs) {
      out.overdue.push(d);
      continue;
    }
    const diff = localDayNumber(dueMs, tz) - nowDay;
    if (diff === 0) out.today.push(d);
    else if (diff >= 1 && diff <= 6) out.week.push(d);
    else out.later.push(d);
  }

  for (const segment of SEGMENTS) {
    out[segment].sort((a, b) => Date.parse(a.due_at) - Date.parse(b.due_at));
  }
  return out;
}

/**
 * Ближайший активный дедлайн (hero-карточка). Просроченные имеют приоритет:
 * показываем самый ранний из них — он требует действия раньше остальных.
 */
export function nearestDeadline(list: readonly Deadline[], now: Date | number): Deadline | null {
  const nowMs = now instanceof Date ? now.getTime() : now;
  let best: Deadline | null = null;
  let bestOverdue = false;
  let bestMs = 0;

  for (const d of list) {
    if (d.status !== 'active') continue;
    const ms = Date.parse(d.due_at);
    if (Number.isNaN(ms)) continue;

    const overdue = ms < nowMs;
    // Просроченный всегда важнее будущего; среди однотипных — самый ранний.
    const better =
      best === null ||
      (overdue && !bestOverdue) ||
      (overdue === bestOverdue && ms < bestMs);
    if (better) {
      best = d;
      bestOverdue = overdue;
      bestMs = ms;
    }
  }
  return best;
}

/** Группировка по календарному дню (номер суток в tz) — для календаря и дня. */
export function groupByDay(
  list: readonly Deadline[],
  tz: string,
): Map<number, Deadline[]> {
  const out = new Map<number, Deadline[]>();
  for (const d of list) {
    if (d.status !== 'active') continue;
    const ms = Date.parse(d.due_at);
    if (Number.isNaN(ms)) continue;
    const day = localDayNumber(ms, tz);
    const bucket = out.get(day);
    if (bucket) bucket.push(d);
    else out.set(day, [d]);
  }
  for (const bucket of out.values()) {
    bucket.sort((a, b) => Date.parse(a.due_at) - Date.parse(b.due_at));
  }
  return out;
}

/** Сколько дедлайнов в каждом сегменте (для подписи секции). */
export function segmentCount(segmented: SegmentedDeadlines, segment: Segment): number {
  return segmented[segment].length;
}