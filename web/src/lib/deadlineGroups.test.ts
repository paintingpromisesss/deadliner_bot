// Тесты чистой логики списка дедлайнов: фильтр по типу (личные/групповые),
// раскладка по сегментам (Просрочено/Сегодня/7 дней/Позже), выбор ближайшего и
// группировка по дням для календаря.
//
// now передаётся параметром — границы локальных суток проверяются точно.
import { describe, expect, it } from 'vitest';
import {
  filterByScope,
  groupByDay,
  isGroupDeadline,
  isPersonalDeadline,
  nearestDeadline,
  onlyActive,
  segmentize,
  SEGMENTS,
} from './deadlineGroups';
import type { Deadline } from './deadlines';

const MSK = 'Europe/Moscow';

/** Фабрика дедлайна: поля DTO, важные для логики, плюс безопасные дефолты. */
function mk(
  id: number,
  dueAt: string,
  extra: Partial<Deadline> = {},
): Deadline {
  return {
    id,
    group_id: null,
    owner_user_id: 1,
    title: `Дедлайн ${id}`,
    description: '',
    due_at: dueAt,
    tz: MSK,
    status: 'active',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    ...extra,
  };
}

// now = 2026-09-29 12:00 MSK (09:00 UTC).
const NOW = new Date('2026-09-29T09:00:00Z');

describe('фильтр по типу', () => {
  const personal = mk(1, '2026-09-30T09:00:00Z');
  const group = mk(2, '2026-09-30T09:00:00Z', { group_id: 42, owner_user_id: null });

  it('isGroupDeadline отличает групповой от личного по group_id', () => {
    expect(isGroupDeadline(group)).toBe(true);
    expect(isGroupDeadline(personal)).toBe(false);
    // null и undefined одинаково означают «персональный».
    expect(isGroupDeadline(mk(3, '2026-09-30T09:00:00Z', { group_id: undefined }))).toBe(false);
    expect(isPersonalDeadline(group)).toBe(false);
    expect(isPersonalDeadline(personal)).toBe(true);
  });

  it('filterByScope: all / personal / group', () => {
    const list = [personal, group];
    expect(filterByScope(list, 'all').map((d) => d.id)).toEqual([1, 2]);
    expect(filterByScope(list, 'personal').map((d) => d.id)).toEqual([1]);
    expect(filterByScope(list, 'group').map((d) => d.id)).toEqual([2]);
  });

  it('filterByScope не мутирует исходный список', () => {
    const list = [personal, group];
    filterByScope(list, 'all').push(mk(9, '2026-09-30T09:00:00Z'));
    expect(list).toHaveLength(2);
  });
});

describe('onlyActive', () => {
  it('выполненные и архивные не попадают в списки', () => {
    const list = [
      mk(1, '2026-09-30T09:00:00Z'),
      mk(2, '2026-09-30T09:00:00Z', { status: 'done' }),
      mk(3, '2026-09-30T09:00:00Z', { status: 'archived' }),
    ];
    expect(onlyActive(list).map((d) => d.id)).toEqual([1]);
  });
});

describe('segmentize: сегменты по сроку', () => {
  it('раскладывает по всем четырём сегментам', () => {
    const list = [
      mk(1, '2026-09-30T12:00:00Z'), // завтра → week
      mk(2, '2026-09-29T08:00:00Z'), // час назад → overdue
      mk(3, '2026-09-29T15:00:00Z'), // сегодня вечером → today
      mk(4, '2026-10-20T09:00:00Z'), // далеко → later
    ];
    const s = segmentize(list, MSK, NOW);
    expect(s.overdue.map((d) => d.id)).toEqual([2]);
    expect(s.today.map((d) => d.id)).toEqual([3]);
    expect(s.week.map((d) => d.id)).toEqual([1]);
    expect(s.later.map((d) => d.id)).toEqual([4]);
  });

  it('просроченное «сегодня» попадает в overdue, а не в today', () => {
    const s = segmentize([mk(1, '2026-09-29T08:00:00Z')], MSK, NOW);
    expect(s.overdue).toHaveLength(1);
    expect(s.today).toHaveLength(0);
  });

  it('границы суток считаются в tz пользователя, не в UTC', () => {
    // 2026-09-29 23:00 MSK = 20:00 UTC — те же локальные сутки (today),
    // хотя в UTC это тоже 29-е; проверим обратное: 21:00 UTC = 30.09 00:00 MSK.
    const today = mk(1, '2026-09-29T20:00:00Z');
    const tomorrow = mk(2, '2026-09-29T21:00:00Z');
    const s = segmentize([today, tomorrow], MSK, NOW);
    expect(s.today.map((d) => d.id)).toEqual([1]);
    expect(s.week.map((d) => d.id)).toEqual([2]);

    // В UTC то же второе мгновение — ещё 29-е, то есть «сегодня» и «week»
    // различаются именно сменой tz.
    const utc = segmentize([tomorrow], 'UTC', new Date('2026-09-29T09:00:00Z'));
    expect(utc.today.map((d) => d.id)).toEqual([2]);
  });

  it('«7 дней» — это 6 следующих суток: 6-е сутки внутри, 7-е уже позже', () => {
    const day6 = mk(1, '2026-10-05T09:00:00Z'); // 05.10 12:00 MSK
    const day7 = mk(2, '2026-10-06T09:00:00Z'); // 06.10 12:00 MSK
    const s = segmentize([day6, day7], MSK, NOW);
    expect(s.week.map((d) => d.id)).toEqual([1]);
    expect(s.later.map((d) => d.id)).toEqual([2]);
  });

  it('done и archived исключаются из всех сегментов', () => {
    const list = [
      mk(1, '2026-09-28T09:00:00Z', { status: 'done' }),
      mk(2, '2026-09-29T15:00:00Z', { status: 'done' }),
      mk(3, '2026-10-20T09:00:00Z', { status: 'archived' }),
    ];
    const s = segmentize(list, MSK, NOW);
    for (const segment of SEGMENTS) expect(s[segment]).toHaveLength(0);
  });

  it('внутри сегмента порядок по возрастанию срока', () => {
    const list = [
      mk(3, '2026-09-29T18:00:00Z'),
      mk(1, '2026-09-29T11:00:00Z'),
      mk(2, '2026-09-29T14:00:00Z'),
    ];
    expect(segmentize(list, MSK, NOW).today.map((d) => d.id)).toEqual([1, 2, 3]);
  });

  it('не мутирует входной массив', () => {
    const list = [mk(2, '2026-09-29T18:00:00Z'), mk(1, '2026-09-29T11:00:00Z')];
    segmentize(list, MSK, NOW);
    expect(list.map((d) => d.id)).toEqual([2, 1]);
  });

  it('мусорный due_at не роняет раскладку', () => {
    const list = [mk(1, 'не-дата'), mk(2, '2026-09-29T15:00:00Z')];
    const s = segmentize(list, MSK, NOW);
    expect(s.today.map((d) => d.id)).toEqual([2]);
    expect(s.overdue).toHaveLength(0);
  });

  it('unchanged границы: срок ровно в now — не просрочен (today)', () => {
    const s = segmentize([mk(1, '2026-09-29T09:00:00Z')], MSK, NOW);
    expect(s.overdue).toHaveLength(0);
    expect(s.today).toHaveLength(1);
  });
});

describe('nearestDeadline: hero-карточка', () => {
  it('без просрочек — самый ранний будущий', () => {
    const list = [
      mk(1, '2026-10-01T09:00:00Z'),
      mk(2, '2026-09-30T09:00:00Z'),
      mk(3, '2026-11-01T09:00:00Z'),
    ];
    expect(nearestDeadline(list, NOW)?.id).toBe(2);
  });

  it('при наличии просрочки приоритет у самого раннего просроченного', () => {
    const list = [
      mk(1, '2026-09-29T10:00:00Z'), // ближайший будущий
      mk(2, '2026-09-28T10:00:00Z'), // просрочен раньше всех
      mk(3, '2026-09-29T05:00:00Z'), // просрочен позже
    ];
    expect(nearestDeadline(list, NOW)?.id).toBe(2);
  });

  it('done/archived и пустой список → null', () => {
    expect(nearestDeadline([], NOW)).toBeNull();
    expect(nearestDeadline([mk(1, '2026-09-30T09:00:00Z', { status: 'done' })], NOW)).toBeNull();
  });

  it('единственный дедлайн сразу становится ближайшим', () => {
    expect(nearestDeadline([mk(1, '2026-10-10T09:00:00Z')], NOW)?.id).toBe(1);
  });

  it('срок ровно в now считается будущим (граница строгая)', () => {
    const now = mk(1, '2026-09-30T09:00:00Z');
    expect(nearestDeadline([now], NOW)?.id).toBe(1);
  });
});

describe('groupByDay: раскладка по дням календаря', () => {
  it('группирует по календарному дню tz и сортирует внутри дня', () => {
    const list = [
      mk(1, '2026-09-29T18:00:00Z'), // 29.09 21:00 MSK
      mk(2, '2026-09-29T11:00:00Z'), // 29.09 14:00 MSK
      mk(3, '2026-09-30T09:00:00Z'), // 30.09 12:00 MSK
      mk(4, '2026-09-29T18:00:00Z', { status: 'done' }),
    ];
    const map = groupByDay(list, MSK);
    expect(map.size).toBe(2);
    const day29 = [...map.values()].find((v) => v.some((d) => d.id === 1))!;
    expect(day29.map((d) => d.id)).toEqual([2, 1]);
    expect([...map.values()].flat().map((d) => d.id).sort()).toEqual([1, 2, 3]);
  });

  it('мгновение относится к дню по локальным суткам', () => {
    // 21:00 UTC 29.09 — это уже 00:00 MSK 30.09.
    const map = groupByDay([mk(1, '2026-09-29T21:00:00Z')], MSK);
    const days = [...map.keys()];
    const utcMap = groupByDay([mk(1, '2026-09-29T21:00:00Z')], 'UTC');
    expect(days[0]).not.toBe([...utcMap.keys()][0]);
  });
});