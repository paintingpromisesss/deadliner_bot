// Логика формы дедлайна: состояние полей, валидация и сборка тела запроса.
// Вынесена из компонента, чтобы проверяться тестом без DOM (в том же духе, что
// deadlineGroups.ts).
//
// Соглашения:
//  - дата и время в состоянии формы — «настенные» строки (YYYY-MM-DD, HH:MM) в
//    tz пользователя, ровно как их отдаёт нативный input; в API уходит RFC3339 UTC;
//  - пресеты-чипы — минуты отступа (7д=10080, 3д=4320, 24ч=1440), как в
//    group.default_presets (спека §4);
//  - напоминания: если для группового дедлайна не передать ни одного, сервер
//    молча подставит default_presets группы (app/deadlines.Service.Create) —
//    пользователь получил бы не то, что видит на экране. Поэтому форма требует
//    хотя бы одно напоминание для группового дедлайна.
import type { ReminderInput } from './deadlines';
import { fromWallClock, toDateInputValue, toRFC3339, toTimeInputValue } from './format';
import { strings } from './strings';

/** Пресеты спеки: 7 дней / 3 дня / 24 часа в минутах. */
export const PRESET_MINUTES = [10080, 4320, 1440] as const;

/** Границы DTO-валидации backend (спека §5.2, app/deadlines). */
export const MAX_TITLE = 200;
export const MAX_DESCRIPTION = 2000;
export const MAX_REMINDERS = 10;
export const MIN_OFFSET_MINUTES = 5;
export const MAX_HORIZON_MS = 5 * 365 * 24 * 60 * 60 * 1000;

export type ReminderUnit = 'hours' | 'days';

/** Кастомное напоминание: отступ от срока либо точное время (спека §7.1). */
export type CustomReminder =
  | { id: string; kind: 'custom_offset'; amount: number; unit: ReminderUnit }
  | { id: string; kind: 'custom_at'; date: string; time: string };

export interface DeadlineFormState {
  title: string;
  description: string;
  /** null — личный дедлайн; иначе id группы. */
  groupId: number | null;
  /** YYYY-MM-DD в tz пользователя. */
  date: string;
  /** HH:MM в tz пользователя. */
  time: string;
  /** Включённые пресеты-чипы (минуты). */
  presets: number[];
  custom: CustomReminder[];
}

export interface ValidationErrors {
  title?: string;
  description?: string;
  group?: string;
  due?: string;
  reminders?: string;
  /** id кастомного напоминания → текст ошибки. */
  custom: Record<string, string>;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationErrors;
  /** Срок в UTC (null, если дата/время не разобраны). */
  dueAt: Date | null;
  /** Напоминания для тела запроса. */
  reminders: ReminderInput[];
  /** Ошибок нет вообще, включая кастомные (удобно для disabled у submit). */
  reminderErrors: boolean;
}

/** Пустое состояние для режима создания. */
export function emptyForm(defaults: Partial<DeadlineFormState> = {}): DeadlineFormState {
  return {
    title: '',
    description: '',
    groupId: null,
    date: '',
    time: '23:59',
    presets: [],
    custom: [],
    ...defaults,
  };
}

/** Состояние формы из существующего дедлайна (режим редактирования). */
export function formFromDeadline(
  deadline: { title: string; description: string; group_id?: number | null; due_at: string },
  tz: string,
): DeadlineFormState {
  return emptyForm({
    title: deadline.title,
    description: deadline.description ?? '',
    groupId: deadline.group_id ?? null,
    date: toDateInputValue(deadline.due_at, tz),
    time: toTimeInputValue(deadline.due_at, tz),
  });
}

/**
 * Пресеты, предвыбранные при выборе группы: её default_presets, оставшиеся
 * после пересечения с известными чипами. Пустой набор группы → все три пресета
 * (такое же поведение у backend: defaultPresets в app/deadlines).
 */
export function presetsForGroup(defaultPresets: readonly number[] | undefined): number[] {
  if (!defaultPresets || defaultPresets.length === 0) return [...PRESET_MINUTES];
  const known = PRESET_MINUTES.filter((m) => defaultPresets.includes(m));
  return known.length > 0 ? known : [...PRESET_MINUTES];
}

/** Переключение пресета-чипа (порядок — от большего отступа к меньшему). */
export function togglePreset(presets: readonly number[], minutes: number): number[] {
  return presets.includes(minutes)
    ? presets.filter((m) => m !== minutes)
    : [...presets, minutes].sort((a, b) => b - a);
}

/** Минуты кастомного отступа. */
export function customOffsetMinutes(amount: number, unit: ReminderUnit): number {
  const perUnit = unit === 'days' ? 24 * 60 : 60;
  return Math.round(amount * perUnit);
}

/** Требуется ли хотя бы одно напоминание: групповой дедлайн наследует пресеты группы. */
export function requiresReminders(state: DeadlineFormState): boolean {
  return state.groupId !== null;
}

/** Срок формы в UTC; null — дата/время не заданы или не разобраны. */
export function formDueAt(state: DeadlineFormState, tz: string): Date | null {
  if (!state.date || !state.time) return null;
  const due = fromWallClock(state.date, state.time, tz);
  return Number.isNaN(due.getTime()) ? null : due;
}

export interface ValidationOptions {
  /**
   * Учитывать ли напоминания вовсе. false — режим правки: PATCH /deadlines/{id}
   * напоминаний не принимает (backend сам перегенерирует их от нового due_at),
   * поэтому ни ошибки, ни тела reminders там быть не должно.
   */
  includeReminders?: boolean;
  /**
   * Требовать ли хотя бы одно напоминание. По умолчанию — для группового
   * дедлайна (иначе backend молча подставит пресеты группы).
   */
  requireReminders?: boolean;
}

/**
 * Полная валидация формы. now — параметр (тестируемость): «срок в прошлом» и
 * «точное напоминание в прошлом» проверяются относительно переданного мгновения.
 */
export function validateForm(
  state: DeadlineFormState,
  tz: string,
  now: Date | number,
  opts: ValidationOptions = {},
): ValidationResult {
  const nowMs = now instanceof Date ? now.getTime() : now;
  const includeReminders = opts.includeReminders ?? true;
  const requireReminders = opts.requireReminders ?? requiresReminders(state);
  const errors: ValidationErrors = { custom: {} };

  const title = state.title.trim();
  if (!title) errors.title = strings.sheet.errTitleRequired;
  else if (title.length > MAX_TITLE) errors.title = strings.sheet.errTitleLong;

  if (state.description.length > MAX_DESCRIPTION) {
    errors.description = strings.sheet.errDescriptionLong;
  }

  const dueAt = formDueAt(state, tz);
  if (!dueAt) {
    errors.due = strings.sheet.errDueRequired;
  } else if (dueAt.getTime() <= nowMs) {
    errors.due = strings.sheet.errDuePast;
  } else if (dueAt.getTime() - nowMs > MAX_HORIZON_MS) {
    errors.due = strings.sheet.errDueFar;
  }

  const reminders: ReminderInput[] = [];
  if (includeReminders) {
    for (const minutes of state.presets) {
      if (minutes >= MIN_OFFSET_MINUTES) reminders.push({ kind: 'preset', offset_minutes: minutes });
    }

    for (const item of state.custom) {
      if (item.kind === 'custom_offset') {
        const minutes = customOffsetMinutes(item.amount, item.unit);
        if (!Number.isFinite(minutes) || minutes < MIN_OFFSET_MINUTES) {
          errors.custom[item.id] = strings.sheet.errReminderOffset;
          continue;
        }
        reminders.push({ kind: 'custom_offset', offset_minutes: minutes });
      } else {
        const at = item.date && item.time ? fromWallClock(item.date, item.time, tz) : null;
        if (!at || Number.isNaN(at.getTime()) || at.getTime() <= nowMs) {
          errors.custom[item.id] = strings.sheet.errReminderExactPast;
          continue;
        }
        reminders.push({ kind: 'custom_at', fire_at: toRFC3339(at) });
      }
    }

    if (reminders.length > MAX_REMINDERS) {
      errors.reminders = strings.sheet.errReminderLimit;
    } else if (requireReminders && reminders.length === 0) {
      errors.reminders = strings.sheet.errRemindersRequired;
    }
  }

  const reminderErrors = Object.keys(errors.custom).length > 0;

  return {
    valid: !errors.title && !errors.description && !errors.due && !errors.reminders,
    errors,
    dueAt,
    reminders,
    reminderErrors,
  };
}

/**
 * Тело POST /deadlines (спека §5.2). null — срок не разобран (вызывающий
 * обязан сначала прогнать валидацию).
 */
export function buildCreate(
  state: DeadlineFormState,
  tz: string,
  reminders: ReminderInput[],
): {
  group_id: number | null;
  title: string;
  description: string;
  due_at: string;
  tz: string;
  reminders: ReminderInput[];
} | null {
  const due = formDueAt(state, tz);
  if (!due) return null;
  return {
    group_id: state.groupId,
    title: state.title.trim(),
    description: state.description,
    due_at: toRFC3339(due),
    tz,
    reminders,
  };
}

/**
 * Тело PATCH /deadlines/{id}. Напоминания в этом эндпоинте не передаются:
 * backend сам пересчитывает их от нового due_at (спека §7.1). Тип дедлайна
 * (личный/групповой) неизменяем — в форме поле заблокировано в режиме правки.
 * null — срок не разобран.
 */
export function buildPatch(
  state: DeadlineFormState,
  tz: string,
): { title: string; description: string; due_at: string; tz: string } | null {
  const due = formDueAt(state, tz);
  if (!due) return null;
  return {
    title: state.title.trim(),
    description: state.description,
    due_at: toRFC3339(due),
    tz,
  };
}