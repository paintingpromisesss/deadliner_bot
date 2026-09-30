// Форматирование дат, длительностей и склонений для TMA.
//
// Семантика склонений и «человеческих» длительностей ПОВТОРЯЕТ бэкенд
// (internal/i18n/i18n.go:Plural и internal/platform/scheduler/messages.go:
// humanDuration): напоминание в Telegram и подпись в Mini App должны читаться
// одинаково («Дедлайн через 2 дня» ↔ «осталось 2 дня 3 часа»).
//
// Часовой пояс везде — tz пользователя (спека §5: ввод/вывод в tz, в БД UTC).
// Никаких date-библиотек: весь tz-счёт через Intl.DateTimeFormat.
//
// Все функции «относительного» времени принимают now параметром — иначе их
// нельзя проверить детерминированно (границы суток, просрочка).
import { strings } from './strings';

/** Формы русских числительных: 1 день / 2 дня / 5 дней. */
export interface PluralForms {
  one: string;
  few: string;
  many: string;
}

export const DAY_FORMS: PluralForms = { one: 'день', few: 'дня', many: 'дней' };
export const HOUR_FORMS: PluralForms = { one: 'час', few: 'часа', many: 'часов' };
export const MINUTE_FORMS: PluralForms = { one: 'минуту', few: 'минуты', many: 'минут' };

/**
 * Русская форма числительного: «1 день», «2 дня», «5 дней», «21 день».
 * n%100 в 11..14 → родительный множественный (11 дней, не «11 день»).
 * Знак игнорируется (просрочка считается по модулю).
 *
 * Реализация 1:1 с i18n.Plural на бэкенде — правила языка не должны
 * расходиться между ботом и Mini App.
 */
export function pluralizeRu(n: number, forms: PluralForms): string {
  const abs = Math.abs(n);
  if (abs % 100 >= 11 && abs % 100 <= 14) return forms.many;
  if (abs % 10 === 1) return forms.one;
  if (abs % 10 >= 2 && abs % 10 <= 4) return forms.few;
  return forms.many;
}

/** «2 дня» — число с согласованной формой. Внутренняя: наружу отдаётся
 * humanDuration, который сам подбирает формы под разряд. */
function pluralizeRuNum(n: number, forms: PluralForms): string {
  return `${n} ${pluralizeRu(n, forms)}`;
}

const MS_MINUTE = 60_000;
const MS_HOUR = 60 * MS_MINUTE;
const MS_DAY = 24 * MS_HOUR;

/**
 * Грубая человекочитаемая длительность (|ms|): до минуты — «менее минуты»,
 * дальше два старших разряда. Состав «дни + часы» / «часы + минуты» нужен
 * hero-карточке («осталось 2 дня 3 часа»), тогда как бэкенд в напоминаниях
 * печатает один разряд — это осознанное расхождение в детализации, не в языке.
 */
export function humanDuration(ms: number): string {
  const abs = Math.abs(ms);
  if (abs < MS_MINUTE) return 'менее минуты';
  if (abs < MS_HOUR) {
    const minutes = Math.max(1, Math.floor(abs / MS_MINUTE));
    return pluralizeRuNum(minutes, MINUTE_FORMS);
  }
  if (abs < MS_DAY) {
    const hours = Math.floor(abs / MS_HOUR);
    const minutes = Math.floor((abs % MS_HOUR) / MS_MINUTE);
    const head = pluralizeRuNum(hours, HOUR_FORMS);
    return minutes > 0 ? `${head} ${pluralizeRuNum(minutes, MINUTE_FORMS)}` : head;
  }
  const days = Math.floor(abs / MS_DAY);
  const hours = Math.floor((abs % MS_DAY) / MS_HOUR);
  const head = pluralizeRuNum(days, DAY_FORMS);
  return hours > 0 ? `${head} ${pluralizeRuNum(hours, HOUR_FORMS)}` : head;
}

/** Направление отсчёта относительно срока. */
export type CountdownDirection = 'left' | 'overdue' | 'now';

export interface Countdown {
  direction: CountdownDirection;
  /** Длительность без предлога: «2 дня 3 часа», «менее минуты». */
  duration: string;
}

/**
 * Разбор отсчёта: до срока — 'left', после — 'overdue', в пределах минуты —
 * 'now'. Неразобранный срок не считается просрочкой: NaN-арифметика дала бы
 * враньё («просрочен на NaN дней») — возвращаем 'now' («срок истёк»), а сам
 * срок в строке списка печатается прочерком (formatDueOrDash).
 */
export function countdownTo(due: Date | string | number, now: Date | number): Countdown {
  const dueMs = toMs(due);
  if (Number.isNaN(dueMs)) return { direction: 'now', duration: 'менее минуты' };
  const diff = dueMs - toMs(now);
  if (Math.abs(diff) < MS_MINUTE) return { direction: 'now', duration: 'менее минуты' };
  return {
    direction: diff > 0 ? 'left' : 'overdue',
    duration: humanDuration(diff),
  };
}

/**
 * Подписи отсчёта живут ТОЛЬКО в каталоге строк (strings.ts:
 * countdownLeft/countdownOverdue/countdownDue) — здесь возвращается лишь его
 * длительность, а склейку делает вызывающий через tpl. Вторая реализация тех
 * же формулировок в коде означала бы два источника правды для одного текста.
 */
function toMs(value: Date | string | number): number {
  if (value instanceof Date) return value.getTime();
  if (typeof value === 'number') return value;
  return new Date(value).getTime();
}

/** Кэш форматтеров: Intl.DateTimeFormat дорог в создании, а вызовов много. */
const formatterCache = new Map<string, Intl.DateTimeFormat>();

/**
 * Локаль форматирования. Числовые поля (дд.мм.гггг, чч:мм) собираются вручную
 * из formatToParts, поэтому зависят от локали только текстовые подписи
 * (месяцы, дни недели) — а они обязаны быть русскими: интерфейс TMA русский
 * (спека §1). Инженерные сокращения (GMT+3, сент.) берём из ru-RU.
 */
const LOCALE = 'ru-RU';

/**
 * Форматтер с указанным tz. Неизвестная зона (или пустая строка) → UTC: спека
 * дефолтит на Europe/Moscow, но битый tz из DTO не должен ронять список.
 */
function formatter(tz: string, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const key = `${tz}|${JSON.stringify(options)}`;
  const cached = formatterCache.get(key);
  if (cached) return cached;

  let fmt: Intl.DateTimeFormat;
  try {
    fmt = new Intl.DateTimeFormat(LOCALE, { ...options, timeZone: tz || 'UTC' });
  } catch {
    fmt = new Intl.DateTimeFormat(LOCALE, { ...options, timeZone: 'UTC' });
  }
  formatterCache.set(key, fmt);
  return fmt;
}

interface WallClock {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  second: number;
}

/**
 * Разложение мгновения в «настенные» компоненты указанной зоны. Основа всех
 * остальных форматов: сначала получаем поля в tz, потом собираем строку —
 * так результат не зависит от локали ICU и её разделителей.
 */
function wallClock(value: Date | string | number, tz: string): WallClock {
  const date = new Date(toMs(value));
  const parts = formatter(tz, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(date);

  const get = (type: Intl.DateTimeFormatPartTypes): number => {
    const part = parts.find((p) => p.type === type);
    return part ? Number(part.value) : 0;
  };
  return {
    year: get('year'),
    month: get('month'),
    day: get('day'),
    // hourCycle 'h23' даёт 00..23; страховка от ICU, вернувшей 24.
    hour: get('hour') % 24,
    minute: get('minute'),
    second: get('second'),
  };
}

const pad2 = (n: number): string => String(n).padStart(2, '0');

/** «29.09.2026» в tz пользователя. */
export function formatDate(value: Date | string | number, tz: string): string {
  const w = wallClock(value, tz);
  return `${pad2(w.day)}.${pad2(w.month)}.${w.year}`;
}

/** «23:59» в tz пользователя. */
export function formatTime(value: Date | string | number, tz: string): string {
  const w = wallClock(value, tz);
  return `${pad2(w.hour)}:${pad2(w.minute)}`;
}

/**
 * «29.09.2026 23:59» — основной формат срока в TMA (совпадает с форматом
 * напоминаний бота, спека §6.2).
 */
export function formatDue(value: Date | string | number, tz: string): string {
  return `${formatDate(value, tz)} ${formatTime(value, tz)}`;
}

/**
 * true, если значение вообще разбирается в мгновение. Битый due_at из DTO
 * (null/мусор) даёт NaN, и арифметика вокруг него печатает «NaN.NaN.NaN» и
 * «просрочен на NaN дней» — вызывающий обязан проверить это заранее.
 */
export function isValidInstant(value: Date | string | number): boolean {
  return !Number.isNaN(toMs(value));
}

/** Срок или прочерк: страховка для строк списка с неразобранным due_at. */
export function formatDueOrDash(value: Date | string | number, tz: string): string {
  return isValidInstant(value) ? formatDue(value, tz) : strings.common.unknown;
}

/** Смещение зоны в минутах для данного мгновения (учитывает переходы DST). */
export function tzOffsetMinutes(value: Date | string | number, tz: string): number {
  const instant = toMs(value);
  const w = wallClock(instant, tz);
  const asUTC = Date.UTC(w.year, w.month - 1, w.day, w.hour, w.minute, w.second);
  // Смещение всегда кратно минуте, поэтому округление убирает дробную часть
  // секунд и миллисекунд, потерянных wallClock.
  return Math.round((asUTC - instant) / MS_MINUTE);
}

/** «UTC+3» / «UTC» — подпись зоны рядом со сроком. */
export function tzAbbr(value: Date | string | number, tz: string): string {
  const offset = tzOffsetMinutes(value, tz);
  if (offset === 0) return 'UTC';
  const sign = offset > 0 ? '+' : '−';
  const abs = Math.abs(offset);
  const hours = Math.floor(abs / 60);
  const minutes = abs % 60;
  return `UTC${sign}${hours}${minutes ? `:${pad2(minutes)}` : ''}`;
}

/** «29 сентября» — заголовок выбранного дня в календаре. */
export function formatDayMonth(value: Date | string | number, tz: string): string {
  return formatter(tz, { day: 'numeric', month: 'long' }).format(new Date(toMs(value)));
}

/**
 * «сентябрь 2026» — заголовок месяца календаря. Собираем из formatToParts:
 * готовый формат ru-RU добавляет «г.» («сентябрь 2026 г.»), а в шапке календаря
 * это лишний шум.
 */
export function formatMonthTitle(year: number, month: number, tz: string): string {
  // Первое число месяца в полдень UTC: сдвиг зоны ±14ч не уведёт в соседний месяц.
  const parts = formatter(tz, { month: 'long', year: 'numeric' }).formatToParts(
    new Date(Date.UTC(year, month - 1, 1, 12)),
  );
  const name = parts.find((p) => p.type === 'month')?.value ?? '';
  const y = parts.find((p) => p.type === 'year')?.value ?? String(year);
  return `${name} ${y}`;
}

/** «29 сент.» — компактная подпись в списках дня. Не используется экранами
 * (там formatDue/formatDayMonth); оставлена для полноты форматного слоя. */
export function formatDayShort(value: Date | string | number, tz: string): string {
  return formatter(tz, { day: 'numeric', month: 'short' }).format(new Date(toMs(value)));
}

/** Календарный номер дня в tz: «2026-09-29» (ключ группировки по дням). */
export function localDayKey(value: Date | string | number, tz: string): string {
  const w = wallClock(value, tz);
  return `${w.year}-${pad2(w.month)}-${pad2(w.day)}`;
}

/** Сегодняшняя календарная дата в tz пользователя (для календаря/дефолтов формы). */
export function localToday(tz: string, now: Date | number): { year: number; month: number; day: number } {
  const w = wallClock(now, tz);
  return { year: w.year, month: w.month, day: w.day };
}

/** Номер суток (дней от эпохи) для календарной даты мгновения в tz. */
export function localDayNumber(value: Date | string | number, tz: string): number {
  const w = wallClock(value, tz);
  return Math.floor(Date.UTC(w.year, w.month - 1, w.day) / MS_DAY);
}

/** Подписи дней недели, начиная с понедельника (для шапки календаря). */
// Фиксированный список, а не Intl: в русской локали сокращения устойчивы, а
// порядок Пн..Вс не должен зависеть от настроек среды ICU.
export const WEEKDAY_LABELS: readonly string[] = ['пн', 'вт', 'ср', 'чт', 'пт', 'сб', 'вс'];

/** Дни недели месяца в порядке Пн..Вс — индекс 0..6.
 * Только тесты: у экранов день недели приходит из buildMonth (calendar.ts). */
export function weekdayIndexMondayFirst(value: Date | string | number, tz: string): number {
  const w = wallClock(value, tz);
  const dow = new Date(Date.UTC(w.year, w.month - 1, w.day)).getUTCDay(); // 0=Вс
  return (dow + 6) % 7;
}

/**
 * true, если мгновение уже прошло (строго раньше now).
 * Только тесты: экраны сравнивают направления через countdownTo — эта форма
 * нужна там, где важен голый предикат без длительности.
 */
export function isOverdue(due: Date | string | number, now: Date | number): boolean {
  return toMs(due) < toMs(now);
}

/**
 * true, если срок попадает в текущие календарные сутки пользователя.
 * Только тесты: сегментация экрана идёт через localDayNumber в
 * deadlineGroups.segmentize.
 */
export function isToday(due: Date | string | number, tz: string, now: Date | number): boolean {
  return localDayNumber(due, tz) === localDayNumber(now, tz);
}

/**
 * true, если срок — «в пределах 7 дней»: завтра..+6 суток включительно.
 * Сегодняшние дедлайны принадлежат сегменту «Сегодня», а не «7 дней», поэтому
 * окно начинается со следующих суток. Только тесты — см. isToday.
 */
export function isThisWeek(due: Date | string | number, tz: string, now: Date | number): boolean {
  const diff = localDayNumber(due, tz) - localDayNumber(now, tz);
  return diff >= 1 && diff <= 6;
}

/**
 * Значение для <input type="datetime-local" date-part>: «2026-09-29» в tz.
 * (Нативный datetime-local не умеет tz, поэтому дата и время — два поля.)
 */
export function toDateInputValue(value: Date | string | number, tz: string): string {
  const w = wallClock(value, tz);
  return `${w.year}-${pad2(w.month)}-${pad2(w.day)}`;
}

/** Значение для <input type="time">: «23:59» в tz. */
export function toTimeInputValue(value: Date | string | number, tz: string): string {
  return formatTime(value, tz);
}

/**
 * Обратное преобразование: настенное время в зоне tz → UTC-мгновение.
 *
 * Наивная сборка через Date.UTC даёт «как если бы это был UTC», после чего
 * вычитается смещение зоны для полученного мгновения. Второй проход нужен на
 * границах перехода DST, где смещение зависит от самого результата. Для
 * несуществующего локального времени (час, «съеденный» переходом) результат —
 * ближайшее валидное мгновение; это осознанное поведение, а не ошибка.
 */
export function fromWallClock(dateValue: string, timeValue: string, tz: string): Date {
  const [year = 1970, month = 1, day = 1] = dateValue.split('-').map(Number);
  const [hour = 0, minute = 0] = timeValue.split(':').map(Number);
  const naive = Date.UTC(year, month - 1, day, hour, minute, 0);

  let instant = naive;
  for (let i = 0; i < 2; i += 1) {
    instant = naive - tzOffsetMinutes(instant, tz) * MS_MINUTE;
  }
  return new Date(instant);
}

/** RFC3339 (как ждёт API: `due_at` — RFC3339). */
export function toRFC3339(value: Date | number): string {
  return new Date(toMs(value)).toISOString();
}