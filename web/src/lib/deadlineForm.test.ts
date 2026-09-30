// Тесты логики формы дедлайна: валидация (заголовок, срок в прошлом/далеко,
// напоминания), предзаполнение из дедлайна, пресеты группы и сборка тел
// запросов POST/PATCH.
import { describe, expect, it } from 'vitest';
import {
  MAX_HORIZON_MS,
  PRESET_MINUTES,
  buildCreate,
  buildPatch,
  customOffsetMinutes,
  emptyForm,
  formDueAt,
  formFromDeadline,
  presetsForGroup,
  requiresReminders,
  togglePreset,
  validateForm,
} from './deadlineForm';
import { strings } from './strings';

const MSK = 'Europe/Moscow';
// now = 2026-09-29 12:00 MSK.
const NOW = new Date('2026-09-29T09:00:00Z');

describe('emptyForm / formFromDeadline', () => {
  it('создание: пустые поля, время по умолчанию 23:59, тип личный', () => {
    const form = emptyForm();
    expect(form.title).toBe('');
    expect(form.description).toBe('');
    expect(form.groupId).toBeNull();
    expect(form.date).toBe('');
    expect(form.time).toBe('23:59');
    expect(form.presets).toEqual([]);
    expect(form.custom).toEqual([]);
  });

  it('редактирование: срок переводится в tz пользователя', () => {
    const form = formFromDeadline(
      {
        title: 'Курсовая',
        description: 'детали',
        group_id: 42,
        due_at: '2026-09-29T20:59:00Z', // 23:59 MSK
      },
      MSK,
    );
    expect(form.title).toBe('Курсовая');
    expect(form.description).toBe('детали');
    expect(form.groupId).toBe(42);
    expect(form.date).toBe('2026-09-29');
    expect(form.time).toBe('23:59');
  });

  it('редактирование в другой зоне: дата считается в ней', () => {
    const form = formFromDeadline(
      { title: 'X', description: '', due_at: '2026-09-29T20:59:00Z' },
      'Asia/Yakutsk',
    );
    expect(form.date).toBe('2026-09-30');
    expect(form.time).toBe('05:59');
  });
});

describe('пресеты', () => {
  it('togglePreset включает/выключает и держит порядок от большего отступа', () => {
    let presets: number[] = [];
    presets = togglePreset(presets, 1440);
    expect(presets).toEqual([1440]);
    presets = togglePreset(presets, 10080);
    expect(presets).toEqual([10080, 1440]);
    presets = togglePreset(presets, 4320);
    expect(presets).toEqual([10080, 4320, 1440]);
    presets = togglePreset(presets, 4320);
    expect(presets).toEqual([10080, 1440]);
  });

  it('presetsForGroup: пресеты группы, неизвестные отбрасываются', () => {
    expect(presetsForGroup([10080, 4320, 1440])).toEqual([10080, 4320, 1440]);
    expect(presetsForGroup([1440])).toEqual([1440]);
    expect(presetsForGroup([60, 1440])).toEqual([1440]);
    // Пустой набор группы / совсем незнакомый → все три пресета (как backend).
    expect(presetsForGroup([])).toEqual([...PRESET_MINUTES]);
    expect(presetsForGroup(undefined)).toEqual([...PRESET_MINUTES]);
    expect(presetsForGroup([60])).toEqual([...PRESET_MINUTES]);
  });

  it('customOffsetMinutes переводит часы и дни в минуты', () => {
    expect(customOffsetMinutes(2, 'hours')).toBe(120);
    expect(customOffsetMinutes(3, 'days')).toBe(4320);
    expect(customOffsetMinutes(0.5, 'hours')).toBe(30);
  });
});

describe('validateForm: заголовок', () => {
  const base = emptyForm({ title: 'Задача', date: '2026-09-30', time: '10:00' });

  it('пустой заголовок — ошибка, форма не валидна', () => {
    const res = validateForm(emptyForm({ date: '2026-09-30', time: '10:00' }), MSK, NOW);
    expect(res.valid).toBe(false);
    expect(res.errors.title).toBe(strings.sheet.errTitleRequired);
  });

  it('заголовок из пробелов не считается заполненным', () => {
    const res = validateForm({ ...base, title: '   ' }, MSK, NOW);
    expect(res.errors.title).toBe(strings.sheet.errTitleRequired);
  });

  it('заголовок ровно 200 символов проходит, 201 — нет', () => {
    expect(validateForm({ ...base, title: 'a'.repeat(200) }, MSK, NOW).errors.title).toBeUndefined();
    expect(validateForm({ ...base, title: 'a'.repeat(201) }, MSK, NOW).errors.title).toBe(
      strings.sheet.errTitleLong,
    );
  });

  it('описание длиннее 2000 символов — ошибка', () => {
    const res = validateForm({ ...base, description: 'a'.repeat(2001) }, MSK, NOW);
    expect(res.errors.description).toBe(strings.sheet.errDescriptionLong);
  });
});

describe('validateForm: срок', () => {
  it('без даты/времени — «укажите дату и время»', () => {
    const res = validateForm(emptyForm({ title: 'X' }), MSK, NOW);
    expect(res.errors.due).toBe(strings.sheet.errDueRequired);
    expect(res.dueAt).toBeNull();
  });

  it('срок в прошлом блокирует отправку', () => {
    // 2026-09-29 11:59 MSK — на минуту раньше now.
    const res = validateForm(
      emptyForm({ title: 'X', date: '2026-09-29', time: '11:59' }),
      MSK,
      NOW,
    );
    expect(res.valid).toBe(false);
    expect(res.errors.due).toBe(strings.sheet.errDuePast);
  });

  it('срок ровно «сейчас» тоже считается прошедшим', () => {
    const res = validateForm(
      emptyForm({ title: 'X', date: '2026-09-29', time: '12:00' }),
      MSK,
      NOW,
    );
    expect(res.errors.due).toBe(strings.sheet.errDuePast);
  });

  it('будущий срок проходит и превращается в UTC', () => {
    const res = validateForm(
      emptyForm({ title: 'X', date: '2026-09-29', time: '23:59' }),
      MSK,
      NOW,
    );
    expect(res.valid).toBe(true);
    expect(res.dueAt?.toISOString()).toBe('2026-09-29T20:59:00.000Z');
  });

  it('срок дальше 5 лет блокируется', () => {
    const far = new Date(NOW.getTime() + MAX_HORIZON_MS + 86_400_000);
    const res = validateForm(
      emptyForm({
        title: 'X',
        date: far.toISOString().slice(0, 10),
        time: '12:00',
      }),
      'UTC',
      NOW,
    );
    expect(res.errors.due).toBe(strings.sheet.errDueFar);
  });
});

describe('validateForm: напоминания', () => {
  const due = { date: '2026-09-30', time: '10:00' };

  it('личный дедлайн без напоминаний валиден (backend ничего не подставит)', () => {
    const res = validateForm(emptyForm({ title: 'X', ...due }), MSK, NOW);
    expect(res.valid).toBe(true);
    expect(res.reminders).toEqual([]);
  });

  it('групповой дедлайн требует хотя бы одно напоминание (иначе пресеты группы неявны)', () => {
    const res = validateForm(emptyForm({ title: 'X', groupId: 7, ...due }), MSK, NOW);
    expect(res.valid).toBe(false);
    expect(res.errors.reminders).toBe(strings.sheet.errRemindersRequired);
  });

  it('пресеты и кастомные отступы собираются в reminders[]', () => {
    const form = emptyForm({
      title: 'X',
      groupId: 7,
      presets: [10080, 1440],
      custom: [{ id: 'c1', kind: 'custom_offset', amount: 2, unit: 'hours' }],
      ...due,
    });
    const res = validateForm(form, MSK, NOW);
    expect(res.valid).toBe(true);
    expect(res.reminders).toEqual([
      { kind: 'preset', offset_minutes: 10080 },
      { kind: 'preset', offset_minutes: 1440 },
      { kind: 'custom_offset', offset_minutes: 120 },
    ]);
  });

  it('точное время переводится в RFC3339 UTC', () => {
    const form = emptyForm({
      title: 'X',
      groupId: 7,
      custom: [{ id: 'c1', kind: 'custom_at', date: '2026-09-29', time: '23:00' }],
      ...due,
    });
    const res = validateForm(form, MSK, NOW);
    expect(res.valid).toBe(true);
    expect(res.reminders).toEqual([{ kind: 'custom_at', fire_at: '2026-09-29T20:00:00.000Z' }]);
  });

  it('точное напоминание в прошлом — ошибка у конкретного элемента', () => {
    const form = emptyForm({
      title: 'X',
      groupId: 7,
      custom: [{ id: 'c1', kind: 'custom_at', date: '2026-09-29', time: '10:00' }],
      ...due,
    });
    const res = validateForm(form, MSK, NOW);
    expect(res.valid).toBe(false);
    expect(res.errors.custom.c1).toBe(strings.sheet.errReminderExactPast);
    expect(res.reminderErrors).toBe(true);
  });

  it('отступ меньше 5 минут — ошибка', () => {
    // Пресет с отступом <5 минут до backend не доходит вовсе: форма не даёт
    // его «включить» — он просто не попадает в reminders[]. Для личного
    // дедлайна это не блокирует отправку (набор напоминаний необязателен).
    const form = emptyForm({
      title: 'X',
      presets: [1],
      ...due,
    });
    const res = validateForm(form, MSK, NOW);
    expect(res.reminders).toEqual([]);

    const custom = emptyForm({
      title: 'X',
      custom: [{ id: 'c1', kind: 'custom_offset', amount: 1, unit: 'hours' }],
      ...due,
    });
    // 1 час = 60 минут ≥ 5 → валидно.
    expect(validateForm(custom, MSK, NOW).valid).toBe(true);
  });

  it('некорректное кастомное напоминание блокирует отправку (не выпадает молча)', () => {
    // Раньше такой элемент молча выпадал из reminders[], и форма отправлялась
    // с набором, отличным от показанного на экране. Теперь это ошибка.
    const valid = emptyForm({
      title: 'X',
      custom: [{ id: 'c1', kind: 'custom_offset', amount: 1, unit: 'days' }],
      ...due,
    });
    // 0 часов = 0 минут < 5 → ошибка у элемента, отправка заблокирована.
    const zero = emptyForm({
      title: 'X',
      custom: [{ id: 'c1', kind: 'custom_offset', amount: 0, unit: 'hours' }],
      ...due,
    });
    const res = validateForm(zero, MSK, NOW);
    expect(res.errors.custom.c1).toBe(strings.sheet.errReminderOffset);
    expect(res.reminderErrors).toBe(true);
    expect(res.valid).toBe(false);
    // Корректный набор с кастомным отступом по-прежнему валиден.
    expect(validateForm(valid, MSK, NOW).valid).toBe(true);
  });

  it('больше 10 напоминаний — ошибка', () => {
    const form = emptyForm({
      title: 'X',
      groupId: 7,
      custom: Array.from({ length: 11 }, (_, i) => ({
        id: `c${i}`,
        kind: 'custom_offset' as const,
        amount: i + 1,
        unit: 'hours' as const,
      })),
      ...due,
    });
    const res = validateForm(form, MSK, NOW);
    expect(res.errors.reminders).toBe(strings.sheet.errReminderLimit);
    expect(res.valid).toBe(false);
  });

  it('requiresReminders зависит от типа', () => {
    expect(requiresReminders(emptyForm())).toBe(false);
    expect(requiresReminders(emptyForm({ groupId: 1 }))).toBe(true);
  });

  it('режим правки (includeReminders: false) не проверяет и не собирает напоминания', () => {
    // PATCH /deadlines/{id} напоминаний не принимает: даже групповой дедлайн
    // без единого напоминания в правке валиден.
    const form = emptyForm({ title: 'X', groupId: 7, ...due });
    const edit = validateForm(form, MSK, NOW, { includeReminders: false });
    expect(edit.valid).toBe(true);
    expect(edit.reminders).toEqual([]);
    expect(edit.errors.reminders).toBeUndefined();
    // Тот же набор в режиме создания — ошибка про напоминания.
    expect(validateForm(form, MSK, NOW).valid).toBe(false);
  });

  it('режим правки всё равно проверяет заголовок и срок', () => {
    const noTitle = validateForm(
      emptyForm({ ...due }),
      MSK,
      NOW,
      { includeReminders: false },
    );
    expect(noTitle.errors.title).toBe(strings.sheet.errTitleRequired);

    const past = validateForm(
      emptyForm({ title: 'X', date: '2026-09-29', time: '11:00' }),
      MSK,
      NOW,
      { includeReminders: false },
    );
    expect(past.errors.due).toBe(strings.sheet.errDuePast);
  });
});

describe('formDueAt / сборка тел запросов', () => {
  const form = emptyForm({
    title: '  Курсовая  ',
    description: 'детали',
    groupId: 42,
    date: '2026-09-29',
    time: '23:59',
    presets: [1440],
    custom: [{ id: 'c1', kind: 'custom_offset', amount: 3, unit: 'days' }],
  });

  it('formDueAt переводит настенное время в UTC', () => {
    expect(formDueAt(form, MSK)?.toISOString()).toBe('2026-09-29T20:59:00.000Z');
    expect(formDueAt(emptyForm(), MSK)).toBeNull();
  });

  it('buildCreate: тело POST /deadlines с группой, tz и напоминаниями', () => {
    const res = validateForm(form, MSK, NOW);
    const body = buildCreate(form, MSK, res.reminders);
    expect(body).toEqual({
      group_id: 42,
      title: 'Курсовая',
      description: 'детали',
      due_at: '2026-09-29T20:59:00.000Z',
      tz: MSK,
      reminders: [
        { kind: 'preset', offset_minutes: 1440 },
        { kind: 'custom_offset', offset_minutes: 4320 },
      ],
    });
  });

  it('buildCreate: личный дедлайн — group_id null', () => {
    const personal = emptyForm({ title: 'X', date: '2026-09-30', time: '10:00' });
    const res = validateForm(personal, MSK, NOW);
    expect(buildCreate(personal, MSK, res.reminders)?.group_id).toBeNull();
  });

  it('buildCreate возвращает null, если срок не разобран', () => {
    expect(buildCreate(emptyForm({ title: 'X' }), MSK, [])).toBeNull();
  });

  it('buildPatch: только поля PATCH (без reminders и group_id)', () => {
    const patch = buildPatch(form, MSK);
    expect(patch).toEqual({
      title: 'Курсовая',
      description: 'детали',
      due_at: '2026-09-29T20:59:00.000Z',
      tz: MSK,
    });
    expect(Object.keys(patch!)).not.toContain('reminders');
    expect(Object.keys(patch!)).not.toContain('group_id');
  });

  it('buildPatch возвращает null при незаданной дате', () => {
    expect(buildPatch(emptyForm({ title: 'X' }), MSK)).toBeNull();
  });
});