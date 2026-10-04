// Арифметика месячной сетки календаря (спека §9, экран 3): недели Пн..Вс,
// ячейки-дни месяца и «хвосты» соседних месяцев. Чистая функция без Date-библиотек
// и без учёта tz — сетка строится по КАЛЕНДАРНЫМ числам, а не по мгновениям;
// привязка к tz происходит при раскладке дедлайнов по дням (deadlineGroups.ts)
// и при построении границ запроса (monthBounds).
import { fromWallClock } from './format';

/** День сетки: полная дата + признак «принадлежит показываемому месяцу». */
export interface MonthDay {
  year: number;
  /** 1..12 — как в API/Date-конвенции «месяц с единицы». */
  month: number;
  /** 1..31. */
  day: number;
  /** false для добивок в начале/конце (дни соседних месяцев). */
  inMonth: boolean;
  /** 0 = Пн … 6 = Вс. */
  weekday: number;
}

export interface MonthGrid {
  year: number;
  month: number;
  /** Недели по 7 дней, Пн..Вс. */
  weeks: MonthDay[][];
  /** 1..31 — число дней в месяце. */
  daysInMonth: number;
}

const MONTH_DAYS = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31] as const;

export function isLeapYear(year: number): boolean {
  return (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
}

/** Число дней в месяце (месяц 1..12). */
export function daysInMonth(year: number, month: number): number {
  if (month === 2 && isLeapYear(year)) return 29;
  return MONTH_DAYS[month - 1] ?? 30;
}

/** День недели 0..6 (0 = Пн) для календарной даты. Дата берётся в UTC — tz не участвует. */
export function weekdayOf(year: number, month: number, day: number): number {
  // 0 = воскресенье в Date.getUTCDay(); сдвигаем к понедельнику.
  return (new Date(Date.UTC(year, month - 1, day)).getUTCDay() + 6) % 7;
}

/** Прибавление месяцев с нормализацией года (month — 1..12, допускает выход за границы). */
export function addMonths(year: number, month: number, delta: number): { year: number; month: number } {
  const total = year * 12 + (month - 1) + delta;
  return { year: Math.floor(total / 12), month: (total % 12) + 1 };
}

/**
 * Сетка месяца. Строк ровно столько, сколько нужно для покрытия месяца:
 * 4 (февраль, начавшийся с понедельника в невисокосный год), 5 или 6.
 * Первая и последняя недели добиваются днями соседних месяцев (inMonth=false),
 * чтобы получить прямоугольник 7×N.
 */
export function buildMonth(year: number, month: number): MonthGrid {
  const days = daysInMonth(year, month);
  const lead = weekdayOf(year, month, 1);
  const rows = Math.ceil((lead + days) / 7);

  const weeks: MonthDay[][] = [];
  let cursor = new Date(Date.UTC(year, month - 1, 1 - lead)); // начало первой недели

  for (let r = 0; r < rows; r += 1) {
    const week: MonthDay[] = [];
    for (let i = 0; i < 7; i += 1) {
      const y = cursor.getUTCFullYear();
      const m = cursor.getUTCMonth() + 1;
      const d = cursor.getUTCDate();
      week.push({
        year: y,
        month: m,
        day: d,
        inMonth: y === year && m === month,
        weekday: (cursor.getUTCDay() + 6) % 7,
      });
      cursor = new Date(Date.UTC(y, m - 1, d + 1));
    }
    weeks.push(week);
  }

  return { year, month, weeks, daysInMonth: days };
}

/** Ключ дня сетки: «2026-09-29» (совпадает с localDayKey из format.ts). */
export function dayKey(value: Pick<MonthDay, 'year' | 'month' | 'day'>): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${value.year}-${pad(value.month)}-${pad(value.day)}`;
}

/**
 * Номер суток (дней от эпохи) для календарной даты. Совпадает с
 * localDayNumber из format.ts для того же дня в tz — ключ раскладки дедлайнов.
 */
export function dayNumber(year: number, month: number, day: number): number {
  return Math.floor(Date.UTC(year, month - 1, day) / 86_400_000);
}

/**
 * Границы месяца в UTC для запроса from/to (спека §5.2): начало первого дня
 * и последняя миллисекунда последнего, посчитанные через общий fromWallClock —
 * та же логика, что в форме, поэтому границы не «уезжают» на час.
 */
export function monthBounds(year: number, month: number, tz: string): { from: Date; to: Date } {
  const from = fromWallClock(`${dayKey({ year, month, day: 1 })}`, '00:00', tz);
  const next = addMonths(year, month, 1);
  const end = fromWallClock(`${dayKey({ ...next, day: 1 })}`, '00:00', tz);
  return { from, to: new Date(end.getTime() - 1) };
}
