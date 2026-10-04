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
    /**
     * Совпадения есть, но все — уже мои группы: список моих групп и так выше,
     * поэтому вместо «ничего не найдено» честно говорим, где они.
     */
    searchAllMine: 'Эти группы уже в ваших',
    searchAllMineHint: 'Они перечислены в списке ваших групп выше.',
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
      'Группа создаётся со статусом «ожидает»: вы сразу становитесь её админом. Привяжите чат командой /bind_group в чате группы — группа активируется.',
    /** Подсказка автозаполнения названия из слага. */
    titleAutoHint: 'Название подставлено по номеру группы — можно изменить.',

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
    /** 409 при redeem — исчерпанный лимит использований (IncrementUsed → ErrConflict). */
    errInviteExhausted: 'Инвайт-код исчерпан или отозван.',
    /** 409 при публикации инвайта — код создан, но сообщение не доставлено. */
    errInvitePublishFailed: 'Инвайт создан, но сообщение в чат не доставлено.',
    /** Прочие отказы групп, не сводимые к конкретной причине. */
    errGroupGeneric: 'Не удалось выполнить действие с группой.',

    /** Экран группы. */
    detailTitle: 'Группа',
    statusPending: 'Ожидает привязки',
    statusActive: 'активна',
    statusArchived: 'в архиве',
    membersCount: 'Участников: %s',
    myRole: 'Ваша роль',
    bindingHeader: 'Чат группы',
    bindingNone: 'Чат не привязан',
    bindingHint:
      'Привяжите чат к группе: напишите в нём /bind_group %s — бот должен быть администратором чата. После привязки станет доступна выдача админа.',
    bindingChat: 'Привязан чат: %s',
    bindingTopic: 'Привязан чат: %s (#%s)',
    leave: 'Выйти из группы',
    leaveConfirm: 'Выйти из группы?',
    leaveConfirmHint: 'Вернуться можно будет только по новому инвайт-коду.',
    leaveConfirmUnboundHint:
      'Группа ещё не привязана к чату. При выходе она будет безвозвратно удалена.',
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
    /** -1 в запросе = бессрочный инвайт (no expiration). */
    inviteTTLPersistent: 'Бессрочно',
    inviteTTL7: '7 дней',
    inviteTTL1: '1 день',
    inviteTTL30: '30 дней',
    inviteTTL90: '90 дней',
    inviteTTLCustom: 'Свой срок…',
    inviteTTLHoursLabel: 'Часов (1–2160)',
    inviteTTLHours: '%s ч',
    inviteTTLBounds: 'Срок жизни — от 1 до %s часов (90 дней)',
    invitePublish: 'Опубликовать приглашение в чат группы',
    invitePublishHint: 'Бот отправит в чат группы приглашение с кнопкой вступления.',
    invitePublishNoBinding: 'Недоступно: у группы нет привязанного чата.',
    invitePublished: 'Приглашение опубликовано в чат группы.',
    inviteExpiresNever: 'бессрочно',
    inviteCreated: 'Инвайт-код создан',
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
    inviteStatusActive: 'активен',
    inviteStatusExpired: 'истёк',
    inviteStatusRevoked: 'отозван',
    inviteStatusExhausted: 'исчерпан',
    inviteUsesRemaining: 'осталось %s из %s',
    inviteUsesUnlimited: 'без ограничений',
    inviteEmpty: 'В группе пока нет созданных инвайтов.',

    /** Модерация дедлайнов (заявки участников, pending_approval). */
    moderationHeader: 'Дедлайны на модерации',
    moderationDue: 'срок: %s',
    moderationApprove: 'Одобрить',
    moderationReject: 'Отклонить',
    moderationApproveConfirm: 'Опубликовать дедлайн «%s»?',
    moderationApproveHint:
      'Дедлайн станет виден всей группе и запустит напоминания в чат группы.',
    moderationRejectConfirm: 'Отклонить дедлайн «%s»?',
    moderationRejectHint: 'Автор дедлайна сможет предложить его заново.',
    moderationApprovedNotice: 'Дедлайн «%s» опубликован.',

    /** Danger zone. */
    dangerHeader: 'Опасная зона',
    deleteGroup: 'Удалить группу',
    deleteConfirm: 'Удалить группу %s?',
    deleteConfirmHint:
      'Группа скрывается вместе с групповыми дедлайнами. Действие для всех участников.',
  },

  /** Экран подтверждения вступления по startapp-параметру инвайта. */
  join: {
    title: 'Приглашение в группу',
    hint: 'Вступить в группу в Дедлайнере, чтобы отслеживать дедлайны прямо в приложении?',
    confirm: 'Вступить',
    cancel: 'Отмена',
  },

  settings: {
    title: 'Настройки',
    profileHeader: 'Профиль',
    /** Имя не пришло от Telegram — нейтральная подпись вместо пустой строки. */
    defaultName: 'Пользователь',
    noUsername: 'без username',
    tzHeader: 'Часовой пояс',
    tzFooter: 'В нём показываются даты дедлайнов и напоминаний.',
    tzUseDevice: 'Использовать пояс устройства',
    tzDeviceHint: 'Часовой пояс устройства: %s',
    notificationsHeader: 'Уведомления',
    dmDefault: 'Дублировать напоминания в ЛС',
    dmDefaultHint:
      'Отправлять копии напоминаний о дедлайнах в личные сообщения с ботом. Ниже можно переопределить для отдельных групп.',
    groupsHeader: 'Напоминания по группам',
    groupsFooter:
      'Если в нескольких группах одинаковые дедлайны или вы хотите настроить уведомления точечно: переопределите получение ЛС-напоминаний для конкретной группы. «Сбросить к общему» вернёт дефолтную настройку.',
    groupInherit: 'Сбросить к общему',
    groupOverride: 'индивидуально',
    groupInherited: 'как в общих настройках',
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
    /** Подсказка о модерации для участника, создающего групповой дедлайн. */
    groupModerationHint: 'Дедлайн участника публикуется после подтверждения админом группы.',
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
