// Тесты форматирования: склонения (таблица форм), обратный отсчёт в обе
// стороны, вывод дат в tz пользователя, границы сегментов и ввод времени.
//
// Таблица склонений обязана совпадать с бэкендом (i18n.Plural): бот пишет
// «Дедлайн через 2 дня», карточка в TMA — «осталось 2 дня 3 часа».
import { describe, expect, it } from 'vitest';
import {
  DAY_FORMS,
  HOUR_FORMS,
  MINUTE_FORMS,
  countdownLabel,
  countdownTo,
  formatDate,
  formatDayMonth,
  formatDayShort,
  formatDue,
  formatDueWithTz,
  formatMonthTitle,
  formatTime,
  fromWallClock,
  humanDuration,
  isOverdue,
  isThisWeek,
  isToday,
  localDayKey,
  localDayNumber,
  pluralizeRu,
  tzAbbr,
  tzOffsetMinutes,
  weekdayIndexMondayFirst,
  toDateInputValue,
  toRFC3339,
  toTimeInputValue,
} from './format';

const MSK = 'Europe/Moscow';

describe('pluralizeRu: формы числительных', () => {
  it('таблица 1/2/4/5/11/14/21/22', () => {
    const table: Array<[number, string]> = [
      [1, 'день'],
      [2, 'дня'],
      [4, 'дня'],
      [5, 'дней'],
      [11, 'дней'],
      [12, 'дней'],
      [13, 'дней'],
      [14, 'дней'],
      [21, 'день'],
      [22, 'дня'],
      [24, 'дня'],
      [25, 'дней'],
      [100, 'дней'],
      [101, 'день'],
      [111, 'дней'],
      [121, 'день'],
    ];
    for (const [n, want] of table) {
      expect(pluralizeRu(n, DAY_FORMS), `n=${n}`).toBe(want);
    }
  });

  it('11..14 важнее последней цифры (11 дней, не «11 день»)', () => {
    for (const n of [11, 12, 13, 14, 111, 112, 113, 114]) {
      expect(pluralizeRu(n, DAY_FORMS)).toBe('дней');
    }
    // ...но 115 уже «дней» по общему правилу последней цифры.
    expect(pluralizeRu(115, DAY_FORMS)).toBe('дней');
    expect(pluralizeRu(15, DAY_FORMS)).toBe('дней');
  });

  it('отрицательные числа склоняются по модулю (просрочка считается назад)', () => {
    expect(pluralizeRu(-1, DAY_FORMS)).toBe('день');
    expect(pluralizeRu(-3, DAY_FORMS)).toBe('дня');
    expect(pluralizeRu(-11, DAY_FORMS)).toBe('дней');
    expect(pluralizeRu(-22, HOUR_FORMS)).toBe('часа');
  });

  it('часы и минуты используют тот же алгоритм', () => {
    expect(pluralizeRu(1, HOUR_FORMS)).toBe('час');
    expect(pluralizeRu(2, HOUR_FORMS)).toBe('часа');
    expect(pluralizeRu(5, HOUR_FORMS)).toBe('часов');
    expect(pluralizeRu(21, HOUR_FORMS)).toBe('час');
    expect(pluralizeRu(1, MINUTE_FORMS)).toBe('минуту');
    expect(pluralizeRu(3, MINUTE_FORMS)).toBe('минуты');
    expect(pluralizeRu(10, MINUTE_FORMS)).toBe('минут');
  });
});

describe('humanDuration', () => {
  it('единицы и составные значения', () => {
    expect(humanDuration(30_000)).toBe('менее минуты');
    expect(humanDuration(60_000)).toBe('1 минуту');
    expect(humanDuration(3 * 60_000)).toBe('3 минуты');
    expect(humanDuration(60 * 60_000)).toBe('1 час');
    expect(humanDuration(5 * 60 * 60_000)).toBe('5 часов');
    // Составные: два старших разряда.
    const twoDaysThreeHours = (2 * 24 + 3) * 60 * 60_000;
    expect(humanDuration(twoDaysThreeHours)).toBe('2 дня 3 часа');
    const hourHalf = 90 * 60_000;
    expect(humanDuration(hourHalf)).toBe('1 час 30 минут');
  });

  it('ровные сутки не тянут за собой «0 часов»', () => {
    expect(humanDuration(3 * 24 * 60 * 60_000)).toBe('3 дня');
    expect(humanDuration(11 * 24 * 60 * 60_000)).toBe('11 дней');
  });

  it('знак не влияет на длительность (abs)', () => {
    expect(humanDuration(-(2 * 24 + 3) * 60 * 60_000)).toBe('2 дня 3 часа');
  });
});

describe('countdownTo / countdownLabel', () => {
  const now = new Date('2026-09-29T12:00:00Z');

  it('до срока — left', () => {
    const c = countdownTo('2026-10-01T15:00:00Z', now);
    expect(c.direction).toBe('left');
    expect(c.duration).toBe('2 дня 3 часа');
    expect(countdownLabel('2026-10-01T15:00:00Z', now)).toBe('осталось 2 дня 3 часа');
  });

  it('после срока — overdue', () => {
    const c = countdownTo('2026-09-29T11:00:00Z', now);
    expect(c.direction).toBe('overdue');
    expect(c.duration).toBe('1 час');
    expect(countdownLabel('2026-09-29T11:00:00Z', now)).toBe('просрочен на 1 час');
  });

  it('в пределах минуты — срок истёк (без «просрочен на менее минуты»)', () => {
    expect(countdownTo('2026-09-29T12:00:30Z', now).direction).toBe('now');
    expect(countdownLabel('2026-09-29T12:00:30Z', now)).toBe('срок истёк');
  });
});

describe('formatDue: время в tz пользователя', () => {
  const instant = '2026-09-29T20:59:00Z'; // 23:59 MSK, 20:59 UTC

  it('MSK', () => {
    expect(formatDue(instant, MSK)).toBe('29.09.2026 23:59');
    expect(formatDate(instant, MSK)).toBe('29.09.2026');
    expect(formatTime(instant, MSK)).toBe('23:59');
  });

  it('UTC', () => {
    expect(formatDue(instant, 'UTC')).toBe('29.09.2026 20:59');
  });

  it('дальневосточная зона переводит срок на следующие сутки', () => {
    // 20:59 UTC → 05:59 (+9), 06:59 (+10) и 07:59 (+11) следующих суток.
    expect(formatDue(instant, 'Asia/Yakutsk')).toBe('30.09.2026 05:59');
    expect(formatDue(instant, 'Asia/Vladivostok')).toBe('30.09.2026 06:59');
    expect(formatDue(instant, 'Asia/Magadan')).toBe('30.09.2026 07:59');
  });

  it('битый tz не роняет форматирование — фолбэк на UTC', () => {
    expect(formatDue(instant, 'Nowhere/City')).toBe('29.09.2026 20:59');
    expect(formatDue(instant, '')).toBe('29.09.2026 20:59');
  });

  it('полночь рендерится как 00:00, а не 24:00', () => {
    expect(formatTime('2026-09-28T21:00:00Z', MSK)).toBe('00:00');
    expect(formatTime('2026-09-29T00:00:00Z', 'UTC')).toBe('00:00');
  });
});

describe('смещение и подпись зоны', () => {
  it('MSK = UTC+3, UTC = UTC', () => {
    const instant = '2026-09-29T20:59:00Z';
    expect(tzOffsetMinutes(instant, MSK)).toBe(180);
    expect(tzAbbr(instant, MSK)).toBe('UTC+3');
    expect(tzAbbr(instant, 'UTC')).toBe('UTC');
    expect(tzAbbr(instant, 'Asia/Kolkata')).toBe('UTC+5:30');
  });

  it('formatDueWithTz добавляет подпись зоны', () => {
    expect(formatDueWithTz('2026-09-29T20:59:00Z', MSK)).toBe('29.09.2026 23:59 (UTC+3)');
  });
});

describe('относительные предикаты (границы локальных суток)', () => {
  // now = 01:00 MSK 29 сентября.
  const now = new Date('2026-09-28T22:00:00Z');

  it('isToday — те же календарные сутки в tz, а не окно 24 часа', () => {
    // 23:00 MSK тех же суток (через 22 часа по абсолюту, но те же сутки).
    expect(isToday('2026-09-29T20:00:00Z', MSK, now)).toBe(true);
    // 00:30 MSK 30 сентября — уже не сегодня, хотя до него 23,5 часа.
    expect(isToday('2026-09-29T21:30:00Z', MSK, now)).toBe(false);
    // 00:30 MSK 29 сентября — ещё сегодня, хотя оно уже прошло.
    expect(isToday('2026-09-28T21:30:00Z', MSK, now)).toBe(true);
  });

  it('isToday зависит от tz: то же мгновение в UTC — другой день', () => {
    expect(localDayKey('2026-09-29T20:00:00Z', MSK)).toBe('2026-09-29');
    expect(localDayKey('2026-09-29T20:00:00Z', 'UTC')).toBe('2026-09-29');
    expect(localDayKey('2026-09-28T21:30:00Z', MSK)).toBe('2026-09-29');
    expect(localDayKey('2026-09-28T21:30:00Z', 'UTC')).toBe('2026-09-28');
  });

  it('isThisWeek — завтра..+6 суток, сегодня исключено', () => {
    expect(isThisWeek('2026-09-29T19:00:00Z', MSK, now)).toBe(false); // сегодня
    expect(isThisWeek('2026-09-29T22:00:00Z', MSK, now)).toBe(true); // завтра 01:00 MSK
    expect(isThisWeek('2026-10-05T20:00:00Z', MSK, now)).toBe(true); // 6-е сутки
    expect(isThisWeek('2026-10-05T21:30:00Z', MSK, now)).toBe(false); // 7-е сутки
  });

  it('isThisWeek не считает прошедшие дни', () => {
    expect(isThisWeek('2026-09-28T20:00:00Z', MSK, now)).toBe(false);
  });

  it('isOverdue — строго раньше now', () => {
    expect(isOverdue('2026-09-28T21:59:59Z', now)).toBe(true);
    expect(isOverdue('2026-09-28T22:00:00Z', now)).toBe(false);
    expect(isOverdue('2026-09-28T22:00:01Z', now)).toBe(false);
  });

  it('localDayNumber различает соседние сутки на 1', () => {
    const a = localDayNumber('2026-09-28T21:30:00Z', MSK); // 29.09 00:30 MSK
    const b = localDayNumber('2026-09-29T21:30:00Z', MSK); // 30.09 00:30 MSK
    expect(b - a).toBe(1);
  });
});

describe('календарные подписи', () => {
  it('день, месяц и рабочий день', () => {
    expect(formatDayMonth('2026-09-29T10:00:00Z', MSK)).toBe('29 сентября');
    expect(formatDayShort('2026-09-29T10:00:00Z', MSK)).toBe('29 сент.');
    expect(formatMonthTitle(2026, 9, MSK)).toContain('сентябр');
    expect(formatMonthTitle(2026, 9, MSK)).toContain('2026');
  });

  it('weekdayIndexMondayFirst: 0 = понедельник', () => {
    // 2026-09-28 — понедельник, 2026-09-29 — вторник.
    expect(weekdayIndexMondayFirst(Date.UTC(2026, 8, 28), 'UTC')).toBe(0);
    expect(weekdayIndexMondayFirst(Date.UTC(2026, 8, 29), 'UTC')).toBe(1);
    expect(weekdayIndexMondayFirst(Date.UTC(2026, 8, 27), 'UTC')).toBe(6);
  });
});

describe('ввод времени формы', () => {
  it('значения для нативных input-ов берутся в tz пользователя', () => {
    const instant = '2026-09-29T20:59:00Z';
    expect(toDateInputValue(instant, MSK)).toBe('2026-09-29');
    expect(toTimeInputValue(instant, MSK)).toBe('23:59');
    // В UTC то же мгновение — 29-е, 20:59.
    expect(toDateInputValue(instant, 'UTC')).toBe('2026-09-29');
    expect(toTimeInputValue(instant, 'UTC')).toBe('20:59');
    // В Якутске это уже 30-е.
    expect(toDateInputValue(instant, 'Asia/Yakutsk')).toBe('2026-09-30');
  });

  it('fromWallClock — обратное преобразование к UTC', () => {
    expect(fromWallClock('2026-09-29', '23:59', MSK).toISOString()).toBe('2026-09-29T20:59:00.000Z');
    expect(fromWallClock('2026-09-29', '20:59', 'UTC').toISOString()).toBe('2026-09-29T20:59:00.000Z');
    expect(fromWallClock('2026-09-30', '05:59', 'Asia/Yakutsk').toISOString()).toBe(
      '2026-09-29T20:59:00.000Z',
    );
  });

  it('круговой обход date/time-инпутов не смещает срок', () => {
    const instant = '2026-12-31T21:30:00.000Z'; // 00:30 MSK 1 января
    const back = fromWallClock(
      toDateInputValue(instant, MSK),
      toTimeInputValue(instant, MSK),
      MSK,
    );
    expect(back.toISOString()).toBe(instant);
  });

  it('переход на летнее время учитывается (Берлин, март 2026)', () => {
    // 2026-03-29 02:00→03:00 CEST: 12:00 локально = 10:00 UTC (CEST, +2).
    expect(fromWallClock('2026-03-29', '12:00', 'Europe/Berlin').toISOString()).toBe(
      '2026-03-29T10:00:00.000Z',
    );
    // За день до перехода тот же локальный полдень — 11:00 UTC (CET, +1).
    expect(fromWallClock('2026-03-28', '12:00', 'Europe/Berlin').toISOString()).toBe(
      '2026-03-28T11:00:00.000Z',
    );
  });

  it('toRFC3339 — формат, который ждёт API', () => {
    expect(toRFC3339(new Date('2026-09-29T20:59:00.000Z'))).toBe('2026-09-29T20:59:00.000Z');
  });
});