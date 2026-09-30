// Тесты арифметики месячной сетки календаря: число дней, високосный февраль,
// день недели первого числа, число строк сетки и добивка соседними месяцами.
import { describe, expect, it } from 'vitest';
import {
  addMonths,
  buildMonth,
  dayKey,
  dayNumber,
  daysInMonth,
  isLeapYear,
  monthBounds,
  weekdayOf,
} from './calendar';
import { localDayNumber } from './format';

const MSK = 'Europe/Moscow';

describe('длина месяца', () => {
  it('обычные месяцы', () => {
    expect(daysInMonth(2026, 1)).toBe(31);
    expect(daysInMonth(2026, 4)).toBe(30);
    expect(daysInMonth(2026, 9)).toBe(30);
    expect(daysInMonth(2026, 12)).toBe(31);
  });

  it('февраль: 28 в невисокосный, 29 в високосный', () => {
    expect(daysInMonth(2026, 2)).toBe(28);
    expect(daysInMonth(2028, 2)).toBe(29);
    // Год, делящийся на 100, но не на 400 — не високосный.
    expect(daysInMonth(2100, 2)).toBe(28);
    expect(daysInMonth(2000, 2)).toBe(29);
  });

  it('isLeapYear', () => {
    expect(isLeapYear(2024)).toBe(true);
    expect(isLeapYear(2026)).toBe(false);
    expect(isLeapYear(2100)).toBe(false);
    expect(isLeapYear(2000)).toBe(true);
  });
});

describe('weekdayOf: 0 = понедельник', () => {
  it('известные даты', () => {
    expect(weekdayOf(2026, 9, 28)).toBe(0); // понедельник
    expect(weekdayOf(2026, 9, 1)).toBe(1); // вторник
    expect(weekdayOf(2026, 9, 6)).toBe(6); // воскресенье
    expect(weekdayOf(2026, 3, 1)).toBe(6); // 01.03.2026 — воскресенье
    expect(weekdayOf(2027, 2, 1)).toBe(0); // 01.02.2027 — понедельник
  });
});

describe('addMonths', () => {
  it('переходы через год в обе стороны', () => {
    expect(addMonths(2026, 12, 1)).toEqual({ year: 2027, month: 1 });
    expect(addMonths(2026, 1, -1)).toEqual({ year: 2025, month: 12 });
    expect(addMonths(2026, 9, 4)).toEqual({ year: 2027, month: 1 });
    expect(addMonths(2026, 9, -9)).toEqual({ year: 2025, month: 12 });
    expect(addMonths(2026, 9, 0)).toEqual({ year: 2026, month: 9 });
  });
});

describe('buildMonth', () => {
  it('сентябрь 2026: 30 дней, первое — вторник, сетка 5×7', () => {
    const grid = buildMonth(2026, 9);
    expect(grid.weeks).toHaveLength(5);
    expect(grid.daysInMonth).toBe(30);
    for (const week of grid.weeks) expect(week).toHaveLength(7);
    // Первая ячейка — понедельник 31 августа (добивка до месяца).
    expect(grid.weeks[0][0]).toMatchObject({ day: 31, month: 8, inMonth: false, weekday: 0 });
    expect(grid.weeks[0][1]).toMatchObject({ day: 1, month: 9, inMonth: true, weekday: 1 });
    // Последняя ячейка — воскресенье 4 октября.
    const last = grid.weeks[4][6];
    expect(last).toMatchObject({ day: 4, month: 10, inMonth: false, weekday: 6 });
  });

  it('февраль 2028 (високосный) — 29 дней и добивка', () => {
    const grid = buildMonth(2028, 2);
    expect(grid.daysInMonth).toBe(29);
    const inMonth = grid.weeks.flat().filter((d) => d.inMonth);
    expect(inMonth).toHaveLength(29);
    expect(inMonth[0]).toMatchObject({ day: 1, month: 2 });
    expect(inMonth[28]).toMatchObject({ day: 29, month: 2 });
  });

  it('февраль 2027 начинается с понедельника — ровно 4 строки', () => {
    const grid = buildMonth(2027, 2);
    expect(grid.weeks).toHaveLength(4);
    expect(grid.weeks[0][0]).toMatchObject({ day: 1, month: 2, inMonth: true, weekday: 0 });
    expect(grid.daysInMonth).toBe(28);
  });

  it('март 2026: 6 строк (31 день, начало в воскресенье)', () => {
    const grid = buildMonth(2026, 3);
    expect(grid.weeks).toHaveLength(6);
    expect(grid.weeks.flat().filter((d) => d.inMonth)).toHaveLength(31);
    // Единственная «ложная» ячейка в первой неделе — 23..28 февраля.
    expect(grid.weeks[0].slice(0, 6).every((d) => !d.inMonth && d.month === 2)).toBe(true);
    expect(grid.weeks[0][6]).toMatchObject({ day: 1, month: 3, inMonth: true });
  });

  it('декабрь: добивка в январе следующего года (переход года в сетке)', () => {
    const grid = buildMonth(2026, 12);
    const last = grid.weeks.at(-1)!.at(-1)!;
    expect(last.year).toBe(2027);
    expect(last.month).toBe(1);
    expect(last.inMonth).toBe(false);
  });

  it('каждая неделя начинается понедельником и заканчивается воскресеньем', () => {
    for (const [y, m] of [
      [2026, 1],
      [2026, 2],
      [2026, 8],
      [2028, 2],
      [2027, 2],
      [2026, 12],
    ] as const) {
      const grid = buildMonth(y, m);
      for (const week of grid.weeks) {
        expect(week[0].weekday).toBe(0);
        expect(week[6].weekday).toBe(6);
      }
      // Дни идут подряд без пропусков.
      const flat = grid.weeks.flat();
      for (let i = 1; i < flat.length; i += 1) {
        expect(dayNumber(flat[i].year, flat[i].month, flat[i].day) -
          dayNumber(flat[i - 1].year, flat[i - 1].month, flat[i - 1].day)).toBe(1);
      }
    }
  });

  it('дни месяца покрыты ровно один раз', () => {
    const grid = buildMonth(2026, 9);
    const keys = grid.weeks.flat().filter((d) => d.inMonth).map(dayKey);
    expect(new Set(keys).size).toBe(30);
    expect(keys).toContain('2026-09-01');
    expect(keys).toContain('2026-09-30');
  });
});

describe('monthBounds: границы запроса from/to', () => {
  it('MSK: с 00:00 первого дня по последнюю миллисекунду последнего', () => {
    const { from, to } = monthBounds(2026, 9, MSK);
    expect(from.toISOString()).toBe('2026-08-31T21:00:00.000Z'); // 01.09 00:00 MSK
    expect(to.toISOString()).toBe('2026-09-30T20:59:59.999Z'); // 30.09 23:59:59.999 MSK
  });

  it('UTC: границы совпадают с календарными датами', () => {
    const { from, to } = monthBounds(2026, 9, 'UTC');
    expect(from.toISOString()).toBe('2026-09-01T00:00:00.000Z');
    expect(to.toISOString()).toBe('2026-09-30T23:59:59.999Z');
  });

  it('декабрь: конец месяца попадает в следующий год', () => {
    const { to } = monthBounds(2026, 12, MSK);
    expect(to.toISOString()).toBe('2026-12-31T20:59:59.999Z');
    const jan = monthBounds(2027, 1, MSK);
    expect(jan.from.toISOString()).toBe('2026-12-31T21:00:00.000Z');
  });

  it('границы согласованы с localDayNumber: первый и последний день внутри окна', () => {
    const { from, to } = monthBounds(2028, 2, MSK);
    const first = localDayNumber(from, MSK);
    const last = localDayNumber(to, MSK);
    expect(dayNumber(2028, 2, 1)).toBe(first);
    expect(dayNumber(2028, 2, 29)).toBe(last);
  });
});

describe('dayNumber', () => {
  it('соседние дни отличаются на 1 и совпадают с localDayNumber', () => {
    expect(dayNumber(2026, 9, 29) - dayNumber(2026, 9, 28)).toBe(1);
    expect(dayNumber(2026, 12, 31) + 1).toBe(dayNumber(2027, 1, 1));
    expect(dayNumber(2026, 9, 29)).toBe(localDayNumber(Date.UTC(2026, 8, 29, 12), 'UTC'));
  });
});