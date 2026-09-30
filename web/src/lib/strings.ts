// Каталог строк интерфейса TMA (спека §1: «все строки — через каталог
// сообщений»). Аналог backend-каталога internal/i18n/locales/ru.json: в
// компонентах нет литеральных текстов, только константные ключи отсюда.
//
// Формат значений совпадает с backend: `%s` — позиционные подстановки, поэтому
// в шаблонах и на сервере, и здесь работает один и тот же порядок аргументов.

/** Подстановка позиционных аргументов в шаблон (`%s`), как i18n.T на бэкенде. */
export function tpl(template: string, ...args: (string | number)[]): string {
  let i = 0;
  return template.replace(/%s/g, () => (i < args.length ? String(args[i++]) : '%s'));
}

export const strings = {
  common: {
    loadError: 'Не удалось загрузить данные',
    retry: 'Повторить',
    cancel: 'Отмена',
    close: 'Закрыть',
    /** Прочерк вместо значения, которое не удалось разобрать (битый due_at). */
    unknown: '—',
  },

  deadlines: {
    title: 'Дедлайны',
    /** Подпись hero-карточки «ближайший дедлайн» (спека §9, экран 2). */
    heroLabel: 'Ближайший дедлайн',
    heroLabelOverdue: 'Просроченный дедлайн',
    personal: 'Личный',
    personalShort: 'Личные',
    groupShort: 'Групповые',
    allShort: 'Все',
    /** Фильтр-чипы над списком. */
    filtersLabel: 'Фильтр по типу',
    /** Сегменты по срокам. */
    segmentOverdue: 'Просрочено',
    segmentToday: 'Сегодня',
    segmentWeek: '7 дней',
    segmentLater: 'Позже',
    /** Обратный отсчёт (hero-карточка). */
    countdownLeft: 'осталось %s',
    countdownOverdue: 'просрочен на %s',
    countdownDue: 'срок истёк',
    /** Подпись итоговой строки списка («всего дедлайнов: 4»). */
    totalCount: 'Всего дедлайнов: %s',
    emptyAll: 'Дедлайнов нет',
    emptyAllHint: 'Создайте первый дедлайн кнопкой «+» внизу экрана.',
    emptyFiltered: 'Ничего не найдено',
    emptyFilteredHint: 'В этом фильтре дедлайнов нет — попробуйте «Все».',
    addButton: 'Новый дедлайн',
    complete: 'Отметить выполненным',
    /**
     * Фолбэк текста ошибки действия из списка (когда сервер не прислал
     * сообщение): конкретику даёт конверт ошибки API.
     */
    actionFailed: 'Не удалось выполнить действие',
  },

  calendar: {
    title: 'Календарь',
    prevMonth: 'Предыдущий месяц',
    nextMonth: 'Следующий месяц',
    dayEmptyHeader: 'На этот день дедлайнов нет',
    dayEmptyHint: 'Выберите другой день или создайте дедлайн кнопкой «+».',
    markers: 'дедлайнов: %s',
  },

  sheet: {
    createTitle: 'Новый дедлайн',
    editTitle: 'Дедлайн',
    fieldTitle: 'Заголовок',
    fieldTitlePlaceholder: 'Например: сдать курсовую',
    fieldDescription: 'Описание',
    fieldDescriptionPlaceholder: 'Детали, ссылки, требования',
    fieldType: 'Тип',
    typePersonal: 'Личный',
    typeGroup: 'Групповой',
    noGroups: 'Вы не состоите ни в одной группе — доступны только личные дедлайны.',
    dueHint: 'Время указывается в вашем часовом поясе: %s.',
    fieldDate: 'Дата',
    fieldTime: 'Время',
    remindersHeader: 'Напоминания',
    remindersEditHint: 'Напоминания пересчитываются автоматически при смене срока.',
    reminderPreset7: '7 дней',
    reminderPreset3: '3 дня',
    reminderPreset24: '24 часа',
    reminderAdd: 'Добавить напоминание',
    reminderKindOffset: 'За N до срока',
    reminderKindExact: 'Точное время',
    reminderOffsetValue: 'За сколько',
    reminderOffsetUnit: 'Единица',
    reminderUnitHours: 'часов',
    reminderUnitDays: 'дней',
    /** Подпись напоминания-отступа: «за 7 дней», «за 3 часа», «за 30 минут». */
    reminderOffsetDays: 'за %s дн.',
    reminderOffsetHours: 'за %s ч.',
    reminderOffsetMinutes: 'за %s мин.',
    reminderRemove: 'Удалить напоминание',
    reminderExisting: 'Напоминания',
    reminderExistingEmpty: 'Напоминаний нет',
    reminderStatusPending: 'ожидает',
    reminderStatusSent: 'отправлено',
    reminderStatusCancelled: 'отменено',
    reminderStatusFailed: 'ошибка',
    /** Дубли напоминаний в ЛС (kind='dm_dup', спека §7.3). */
    reminderStatusDmDup: 'дубль в ЛС',
    submitCreate: 'Создать',
    submitSave: 'Сохранить',
    submitViaMainButton: 'Отправка — кнопкой внизу экрана.',
    actionComplete: 'Отметить выполненным',
    actionDelete: 'Удалить',
    confirmDelete: 'Удалить дедлайн?',
    confirmDeleteHint: 'Дедлайн и его напоминания будут удалены без возможности восстановления.',
    metaCreated: 'Создан: %s',
    errTitleRequired: 'Введите заголовок',
    errTitleLong: 'Заголовок — не более 200 символов',
    errDescriptionLong: 'Описание — не более 2000 символов',
    errDueRequired: 'Укажите дату и время',
    errDuePast: 'Срок уже прошёл — выберите будущее время',
    errDueFar: 'Срок — не позднее 5 лет от сейчас',
    errReminderExactPast: 'Время напоминания уже прошло',
    errReminderOffset: 'Минимальный отступ — 5 минут',
    errReminderLimit: 'Не более 10 напоминаний на дедлайн',
    errRemindersRequired: 'Включите хотя бы одно напоминание для группового дедлайна',
    /**
     * Общий блок-заголовок над ошибками конкретных кастомных напоминаний:
     * сами тексты — в errors.custom (errReminderOffset/errReminderExactPast).
     */
    errCustomReminder: 'Исправьте или удалите некорректное напоминание',
  },
} as const;