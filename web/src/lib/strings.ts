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
    /**
     * Фолбэк текста ошибки действия, когда сервер не прислал сообщение
     * (сетевой сбой, HTML вместо конверта).
     */
    actionFailed: 'Не удалось выполнить действие',
  },

  groups: {
    title: 'Группы',
    /** Список моих групп. */
    mineHeader: 'Мои группы',
    empty: 'Вы не состоите ни в одной группе',
    emptyHint:
      'Создайте группу по номеру (М8О-401Б-23) или вступите по инвайт-коду от старосты.',
    createButton: 'Создать группу',
    redeemButton: 'Ввести инвайт-код',
    /** Подпись строки группы: «вы · админ». */
    roleAdmin: 'админ',
    roleMember: 'участник',
    roleNone: 'не участник',
    /** Кнопка возврата к списку из деталей. */
    backToList: 'Все группы',

    /** Поиск/подсказка слага. */
    searchLabel: 'Поиск группы по номеру',
    searchPlaceholder: 'Например: М8О-401Б-23',
    searchEmpty: 'Ничего не найдено',
    searchEmptyHint: 'Проверьте номер: поиск идёт по началу слага.',
    searchJoin: 'Вступить',
    /** Заголовок выпадающего списка подсказок. */
    searchSuggestions: 'Найденные группы',
    /**
     * Поиск показывает и чужие активные группы: вступить в них можно только
     * по инвайт-коду (спека §3.3 — публичного join-эндпоинта нет), поэтому
     * вместо кнопки «вступить» даём честное пояснение.
     */
    searchJoinHint: 'Вступить в группу можно по инвайт-коду от её админа.',

    /** Форма создания. */
    createTitle: 'Новая группа',
    fieldSlug: 'Номер группы (слаг)',
    fieldSlugPlaceholder: 'М8О-401Б-23',
    fieldTitle: 'Название',
    fieldTitlePlaceholder: 'Например: М8О-401Б-23 (ИУ7)',
    slugHint: 'Заглавные буквы, цифры и дефис: 3–16 символов, минимум одна цифра.',
    createSubmit: 'Создать',
    createHint:
      'Группа создаётся со статусом «ожидает»: чат привязывается командой /bind_group, админ — через claim-код.',

    /** Форма ввода инвайт-кода. */
    redeemTitle: 'Инвайт-код',
    fieldCode: 'Код приглашения',
    fieldCodePlaceholder: 'ABCD2345',
    redeemSubmit: 'Вступить',
    redeemHint: 'Код выдаёт админ группы. Регистр не важен.',
    redeemSuccess: 'Вы вступили: %s',

    /** Ошибки клиентской валидации слага (зеркало domain/slug.go). */
    errSlugEmpty: 'Введите номер группы',
    errSlugTooShort: 'Слаг — минимум 3 символа',
    errSlugTooLong: 'Слаг — не более 16 символов',
    errSlugCharset: 'Только заглавные буквы, цифры и дефис (без пустых сегментов)',
    errSlugNoDigit: 'Слаг должен содержать хотя бы одну цифру',
    /** Ошибки заголовка. */
    errTitleRequired: 'Введите название',
    errTitleLong: 'Название — не более 200 символов',
    /** Ошибки от API создания/инвайта. */
    errSlugTaken: 'Такой номер уже занят — возможно, группа создана. Поищите её по номеру.',
    errRateLimit: 'Слишком часто. Попробуйте позже.',
    errRateLimitRetry: 'Слишком часто. Повторить через %s.',
    errCodeUnknown: 'Код не найден, отозван или истёк',

    /** Экран группы. */
    detailTitle: 'Группа',
    statusPending: 'ожидает привязки',
    statusActive: 'активна',
    statusArchived: 'в архиве',
    membersCount: 'Участников: %s',
    myRole: 'Ваша роль',
    bindingHeader: 'Чат группы',
    bindingNone: 'Чат не привязан',
    bindingHint:
      'Привяжите чат к группе: напишите в нём /bind_group %s — бот должен быть администратором чата. После привязки станет доступна выдача админа.',
    bindingChat: 'Привязан чат: %s',
    bindingTopic: 'Привязан чат: %s (топик %s)',
    leave: 'Выйти из группы',
    leaveConfirm: 'Выйти из группы?',
    leaveConfirmHint: 'Вернуться можно будет только по новому инвайт-коду.',
    pendingUntil: 'Группа будет удалена без привязки и админа: %s',

    /** Админ-панель. */
    membersHeader: 'Участники',
    membersEmpty: 'В группе пока только вы',
    promote: 'Сделать админом',
    demote: 'Снять админа',
    kick: 'Исключить',
    kickConfirm: 'Исключить участника?',
    kickConfirmHint: '%s потеряет доступ к групповым дедлайнам этой группы.',
    joinedAt: 'в группе с %s',
    /** Ошибка «последний админ» (API 409 last_admin). */
    errLastAdmin: 'Это последний админ: сначала назначьте другого.',

    /** Инвайты. */
    invitesHeader: 'Инвайт-коды',
    invitesHint: 'Код показывается один раз — скопируйте его сразу после создания.',
    inviteCreate: 'Создать инвайт',
    inviteTitle: 'Новый инвайт',
    inviteRole: 'Роль при вступлении',
    inviteMaxUses: 'Использований',
    inviteMaxUsesUnlimited: 'Без ограничений',
    inviteMaxUsesLimit: 'Не более %s',
    inviteTTL: 'Срок жизни',
    inviteTTL7: '7 дней',
    inviteTTL1: '1 день',
    inviteTTL30: '30 дней',
    inviteTTL90: '90 дней',
    inviteCreated: 'Код создан — скопируйте сейчас',
    inviteCopy: 'Скопировать код',
    inviteCopied: 'Код скопирован',
    inviteRevoke: 'Отозвать',
    inviteRevokeConfirm: 'Отозвать инвайт-код?',
    inviteRevokeConfirmHint: 'По нему больше нельзя будет вступить.',
    inviteSessionOnly:
      'Список показывает коды, созданные в этой сессии: сервер не отдаёт уже выданные коды (в БД хранится только их хэш).',
    inviteExpiresAt: 'до %s',
    inviteRoleAdmin: 'админ при вступлении',
    inviteRoleMember: 'участник',

    /** Claim-флоу (спека §3.1). */
    claimStart: 'Стать админом',
    claimTitle: 'Стать админом группы',
    claimHint:
      'Бот отправит в чат группы одноразовый код. Введите его здесь — код видят все участники чата, поэтому не пересылайте его.',
    claimNoBinding: 'Сначала привяжите чат',
    claimNoBindingHint:
      'Привяжите чат командой /bind_group %s — без привязанного чата код некуда отправить.',
    claimSend: 'Отправить код в чат',
    claimCodeSent: 'Код отправлен в чат',
    claimCodeSentHint: 'Введите 6 цифр из сообщения бота в чат группы.',
    claimCodeLabel: 'Код из чата',
    claimCodePlaceholder: '000000',
    claimConfirm: 'Подтвердить',
    claimExpiresIn: 'Код действует ещё %s',
    claimExpired: 'Срок кода истёк — запросите новый.',
    claimSuccess: 'Готово: вы админ группы %s',
    claimRevoke: 'Отозвать код',
    claimRevokeConfirm: 'Отозвать активный claim-код?',
    claimRevokeConfirmHint:
      'Код в чате перестанет приниматься. Новый можно запросить в любой момент.',
    /**
     * Клиент не знает, есть ли активный код (GET для него в API нет), поэтому
     * админская кнопка отзыва показывается всегда, а её отказ («код не найден»)
     * объясняется прямо в подписи блока.
     */
    claimAdminHint:
      'Если кто-то запросил код смены старосты, он уже опубликован в чате. Погасите его, если смена не согласована. Если активного кода нет, сервер ответит, что отзывать нечего.',
    errClaimNoBinding: 'К группе не привязан чат',
    errClaimSendFailed: 'Не удалось отправить код в чат. Проверьте, что бот — админ чата.',
    errClaimBadCode: 'Неверный код — попробуйте ещё раз',
    errClaimCodeExpired: 'Код не найден или уже истёк — запросите новый',
    errClaimFormat: 'Код — ровно 6 цифр',

    /** Danger zone. */
    dangerHeader: 'Опасная зона',
    deleteGroup: 'Удалить группу',
    deleteConfirm: 'Удалить группу %s?',
    deleteConfirmHint:
      'Группа скрывается вместе с групповыми дедлайнами. Действие для всех участников.',
  },

  settings: {
    title: 'Настройки',
    profileHeader: 'Профиль',
    noUsername: 'без username',
    tzHeader: 'Часовой пояс',
    tzFooter: 'В нём показываются даты дедлайнов и напоминаний.',
    tzUseDevice: 'Использовать пояс устройства',
    tzDeviceHint: 'Часовой пояс устройства: %s',
    notificationsHeader: 'Уведомления',
    dmDefault: 'Дубли в личку по умолчанию',
    dmDefaultHint:
      'Копировать напоминания о групповых дедлайнах в личные сообщения. Ниже можно переопределить для отдельных групп.',
    groupsHeader: 'Дубли по группам',
    groupsFooter:
      'Эффективное значение: своё для группы либо общее по умолчанию. «Наследовать» возвращает группу к общему значению.',
    groupInherit: 'Наследовать',
    groupOverride: 'своё значение',
    groupInherited: 'как по умолчанию',
    groupsEmpty: 'Вы не состоите в группах — настраивать нечего.',
    telegramID: 'Telegram ID',
    save: 'Сохранить',
    saved: 'Сохранено',
    saveFailed: 'Не удалось сохранить',
    logout: 'Выйти',
    loading: 'Настройки',
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