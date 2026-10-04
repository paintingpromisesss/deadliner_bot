// Bottom-sheet формы дедлайна (спека §9, экран 4; создание и редактирование).
//
// Ключевые решения:
//  - состояние полей и валидация живут в lib/deadlineForm.ts (чистые функции,
//    покрыты тестами) — компонент только рендерит и вызывает;
//  - submit дублируется: Telegram MainButton (внутри Telegram) и собственная
//    кнопка внизу шита (вне Telegram, dev-браузер). Кнопки включаются/
//    выключаются одним и тем же признаком valid;
//  - «выполнить» и «удалить» доступны только в режиме правки (спека §9:
//    действия над существующим дедлайном — из его карточки), с подтверждением
//    отдельной модалкой.
import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import {
  Button,
  Caption,
  Divider,
  Input,
  Modal,
  Select,
  Textarea,
} from './ui';
import {
  MAX_DESCRIPTION,
  MAX_TITLE,
  PRESET_MINUTES,
  buildCreate,
  buildPatch,
  emptyForm,
  formFromDeadline,
  presetsForGroup,
  togglePreset,
  validateForm,
  type CustomReminder,
  type DeadlineFormState,
  type ReminderUnit,
} from '../lib/deadlineForm';
import { formatDue, tzAbbr } from '../lib/format';
import type { Deadline, Reminder } from '../lib/deadlines';
import {
  memberGroups,
  adminGroups,
  useCompleteDeadline,
  useCreateDeadline,
  useDeadlineDetail,
  useDeleteDeadline,
  useMyGroups,
  useUpdateDeadline,
} from '../lib/queries';
import { isMainButtonAvailable, showMainButton } from '../lib/tma';
import { strings, tpl } from '../lib/strings';
import { canWriteDeadline } from '../lib/deadlineGroups';
import { useAuthStore } from '../stores/auth';
import { ConfirmDialog } from './ConfirmDialog';
import { PresetChips } from './FilterChips';

interface DeadlineSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** null — создание; иначе редактирование этого дедлайна. */
  deadline: Deadline | null;
  /** Напоминания дедлайна (для блока в режиме правки). */
  reminders?: Reminder[];
  tz: string;
  /** Предзаполненная дата (календарь: тап по дню → «+» на этот день). */
  initialDate?: string;
  /** Вызывается после успешного создания/правки/удаления. */
  onSaved?: () => void;
}

/** Человекочитаемая подпись напоминания (по kind, спека §7.1). */
function reminderLabel(reminder: Reminder, tz: string): string {
  switch (reminder.kind) {
    case 'custom_at':
      return formatDue(reminder.fire_at, tz);
    case 'dm_dup':
      return strings.sheet.reminderStatusDmDup;
    default:
      return reminder.offset_minutes != null
        ? offsetLabel(reminder.offset_minutes)
        : formatDue(reminder.fire_at, tz);
  }
}

function offsetLabel(minutes: number): string {
  if (minutes % 1440 === 0) return tpl(strings.sheet.reminderOffsetDays, minutes / 1440);
  if (minutes % 60 === 0) return tpl(strings.sheet.reminderOffsetHours, minutes / 60);
  return tpl(strings.sheet.reminderOffsetMinutes, minutes);
}

/** Подпись пресета-чипа: только известные спеки-наборы (7д/3д/24ч). */
function presetLabel(minutes: number): string {
  if (minutes === 10080) return strings.sheet.reminderPreset7;
  if (minutes === 4320) return strings.sheet.reminderPreset3;
  return strings.sheet.reminderPreset24;
}

export function DeadlineSheet({
  open,
  onOpenChange,
  deadline,
  reminders,
  tz,
  initialDate,
  onSaved,
}: DeadlineSheetProps) {
  const isEdit = deadline !== null;
  const groups = useMyGroups();
  // Селектор группы — всем участникам: дедлайн участника уйдёт на модерацию.
  const writable = useMemo(() => memberGroups(groups.data), [groups.data]);
  // Права записи считаются по АДМИНСКИМ группам, а не по memberGroups.
  const adminList = useMemo(() => adminGroups(groups.data), [groups.data]);
  const isSuperadmin = useAuthStore((s) => s.user?.is_superadmin ?? false);
  const me = useAuthStore((s) => s.user?.id);
  // «Выполнить»/«удалить» — запись: backend проверяет её requireWrite (автор
  // дедлайна, админ группы или супер-админ — спека §5.2 «автор/admin»).
  // Предикат общий с экраном списка (lib/deadlineGroups.canWriteDeadline): если
  // бы они расходились, кнопка появлялась бы/исчезала при открытии карточки.
  const adminGroupIDs = useMemo(() => new Set(adminList.map((g) => g.group.id)), [adminList]);
  const canAct =
    !deadline ||
    canWriteDeadline(deadline, { me, isSuperadmin, adminGroupIDs });

  const create = useCreateDeadline();
  const update = useUpdateDeadline();
  const remove = useDeleteDeadline();
  const complete = useCompleteDeadline();
  // Напоминания существующего дедлайна — отдельным запросом: DTO списка их не
  // содержит, а блоку «Напоминания» в режиме правки нужны статусы.
  const detail = useDeadlineDetail(isEdit ? (deadline?.id ?? null) : null);
  const existingReminders = reminders ?? detail.data?.reminders ?? [];

  const [form, setForm] = useState<DeadlineFormState>(() =>
    deadline ? formFromDeadline(deadline, tz) : emptyForm({ date: initialDate ?? '' }),
  );
  const [touched, setTouched] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Смена объекта правки/открытие шита — новое состояние формы. Сравниваем по
  // id, а не по ссылке: список перезапросился → ссылка новая, а поля те же, и
  // введённое пользователем затирать нельзя.
  const editingID = deadline?.id ?? null;
  useEffect(() => {
    if (!open) return;
    setForm(deadline ? formFromDeadline(deadline, tz) : emptyForm({ date: initialDate ?? '' }));
    setTouched(false);
    setAttempted(false);
    setSubmitError(null);
    setConfirmDelete(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editingID, tz, initialDate]);

  // Групповой дедлайн: пресеты группы включаются при выборе группы (спека §9),
  // а возврат к личному типу их чистит: иначе набор группы остался бы в форме и
  // ушёл бы в тело POST как напоминания личного дедлайна.
  const selectedGroup = writable.find((g) => g.group.id === form.groupId);
  useEffect(() => {
    if (isEdit) return;
    if (form.groupId === null) {
      setForm((prev) => (prev.presets.length === 0 ? prev : { ...prev, presets: [] }));
      return;
    }
    setForm((prev) => ({ ...prev, presets: presetsForGroup(selectedGroup?.group.default_presets) }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form.groupId, isEdit]);

  const now = useMemo(() => new Date(), [open, editingID]);
  // В режиме правки напоминания не редактируются: PATCH их не принимает, backend
  // пересчитывает набор от нового due_at (спека §7.1). Валидация тогда смотрит
  // только на заголовок, описание и срок.
  const validation = useMemo(
    () => validateForm(form, tz, now, { includeReminders: !isEdit }),
    [form, tz, now, isEdit],
  );
  const showErrors = attempted || touched;
  const canSubmit = validation.valid && !create.isPending && !update.isPending;
  const pending = create.isPending || update.isPending;

  async function submit() {
    setAttempted(true);
    if (!validation.valid) return;
    setSubmitError(null);
    try {
      if (isEdit && deadline) {
        const patch = buildPatch(form, tz);
        if (!patch) return;
        await update.mutateAsync({ id: deadline.id, patch });
      } else {
        const body = buildCreate(form, tz, validation.reminders);
        if (!body) return;
        await create.mutateAsync(body);
      }
      onSaved?.();
      onOpenChange(false);
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : strings.common.loadError);
    }
  }

  async function doDelete() {
    if (!deadline) return;
    setConfirmDelete(false);
    setSubmitError(null);
    try {
      await remove.mutateAsync(deadline.id);
      onSaved?.();
      onOpenChange(false);
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : strings.common.loadError);
    }
  }

  async function doComplete() {
    if (!deadline) return;
    setSubmitError(null);
    try {
      await complete.mutateAsync(deadline.id);
      onSaved?.();
      onOpenChange(false);
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : strings.common.loadError);
    }
  }

  // Telegram MainButton показывает submit, пока шит открыт. Клик по ней идёт
  // мимо React — поэтому обработчик читает актуальные submit/состояние через
  // замыкание эффекта, а зависимости гарантируют пересоздание слушателя.
  const mainButtonAvailable = isMainButtonAvailable();
  useEffect(() => {
    if (!open || !mainButtonAvailable) return;
    return showMainButton({
      text: isEdit ? strings.sheet.submitSave : strings.sheet.submitCreate,
      enabled: canSubmit,
      loading: pending,
      onClick: () => void submit(),
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, mainButtonAvailable, canSubmit, pending, isEdit, form, tz]);

  function patchForm(next: Partial<DeadlineFormState>) {
    setForm((prev) => ({ ...prev, ...next }));
  }

  function addCustom(kind: CustomReminder['kind']) {
    const id = `c${Date.now()}${Math.round(Math.random() * 1000)}`;
    const item: CustomReminder =
      kind === 'custom_offset'
        ? { id, kind: 'custom_offset', amount: 2, unit: 'hours' }
        : { id, kind: 'custom_at', date: form.date, time: '10:00' };
    setForm((prev) => ({ ...prev, custom: [...prev.custom, item] }));
    // Новое напоминание могло получиться некорректным с ходу (точное время в
    // прошлом) — это действие пользователя, поэтому ошибку показываем сразу,
    // а не после первой потери фокуса.
    setTouched(true);
  }

  function updateCustom(id: string, next: Partial<CustomReminder>) {
    setForm((prev) => ({
      ...prev,
      custom: prev.custom.map((item) => (item.id === id ? ({ ...item, ...next } as CustomReminder) : item)),
    }));
  }

  function removeCustom(id: string) {
    setForm((prev) => ({ ...prev, custom: prev.custom.filter((item) => item.id !== id) }));
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      header={<SheetHeader title={isEdit ? strings.sheet.editTitle : strings.sheet.createTitle} onClose={() => onOpenChange(false)} />}
    >
      <div className="dl-sheet" data-testid="deadline-sheet">
        <Input
          header={strings.sheet.fieldTitle}
          placeholder={strings.sheet.fieldTitlePlaceholder}
          value={form.title}
          maxLength={MAX_TITLE}
          status={showErrors && validation.errors.title ? 'error' : 'default'}
          onChange={(e) => patchForm({ title: e.target.value })}
          onBlur={() => setTouched(true)}
          data-testid="field-title"
        />
        {showErrors && validation.errors.title ? (
          <div className="dl-error" role="alert" data-testid="error-title">
            {validation.errors.title}
          </div>
        ) : null}

        <Textarea
          header={strings.sheet.fieldDescription}
          placeholder={strings.sheet.fieldDescriptionPlaceholder}
          value={form.description}
          maxLength={MAX_DESCRIPTION}
          status={showErrors && validation.errors.description ? 'error' : 'default'}
          onChange={(e) => patchForm({ description: e.target.value })}
          data-testid="field-description"
        />
        {showErrors && validation.errors.description ? (
          <div className="dl-error" role="alert">
            {validation.errors.description}
          </div>
        ) : null}

        {/* Тип: личный / группа. В режиме правки заблокирован — тип дедлайна
            неизменяем (PATCH не принимает group_id). */}
        <Select
          header={strings.sheet.fieldType}
          value={form.groupId === null ? '' : String(form.groupId)}
          disabled={isEdit}
          onChange={(e) =>
            patchForm({ groupId: e.target.value === '' ? null : Number(e.target.value) })
          }
          data-testid="field-group"
        >
          <option value="">{strings.sheet.typePersonal}</option>
          {writable.map((g) => (
            <option key={g.group.id} value={g.group.id}>
              {strings.sheet.typeGroup} · {g.group.slug}
            </option>
          ))}
        </Select>
        {writable.length === 0 ? (
          <div className="dl-hint">{strings.sheet.noGroups}</div>
        ) : (
          <div className="dl-hint">{strings.sheet.groupModerationHint}</div>
        )}

        <div className="dl-due-row">
          <Input
            type="date"
            header={strings.sheet.fieldDate}
            value={form.date}
            status={showErrors && validation.errors.due ? 'error' : 'default'}
            onChange={(e) => {
              setTouched(true);
              patchForm({ date: e.target.value });
            }}
            onBlur={() => setTouched(true)}
            data-testid="field-date"
          />
          <Input
            type="time"
            header={strings.sheet.fieldTime}
            value={form.time}
            onChange={(e) => {
              setTouched(true);
              patchForm({ time: e.target.value });
            }}
            onBlur={() => setTouched(true)}
            data-testid="field-time"
          />
        </div>
        {showErrors && validation.errors.due ? (
          <div className="dl-error" role="alert" data-testid="error-due">
            {validation.errors.due}
          </div>
        ) : (
          <div className="dl-hint">{tpl(strings.sheet.dueHint, tzAbbr(now, tz))}</div>
        )}

        <Divider />

        {isEdit ? (
          <div className="dl-reminders">
            <div className="dl-section-header">{strings.sheet.reminderExisting}</div>
            {existingReminders.length > 0 ? (
              <ul className="dl-reminders__list">
                {existingReminders.map((r) => (
                  <li key={r.id}>
                    <span>{reminderLabel(r, tz)}</span>
                    <Caption level="1">{statusLabel(r.status)}</Caption>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="dl-hint">{strings.sheet.reminderExistingEmpty}</div>
            )}
            <div className="dl-hint">{strings.sheet.remindersEditHint}</div>
          </div>
        ) : (
          <>
            <Sectionish header={strings.sheet.remindersHeader}>
              <PresetChips
                label={strings.sheet.remindersHeader}
                options={PRESET_MINUTES.map((minutes) => ({
                  minutes,
                  label: presetLabel(minutes),
                }))}
                selected={form.presets}
                onToggle={(minutes) => {
                  setTouched(true);
                  patchForm({ presets: togglePreset(form.presets, minutes) });
                }}
              />
            </Sectionish>

            {form.custom.map((item) => (
              <div key={item.id} className="dl-custom" data-testid={`custom-${item.id}`}>
                <Select
                  header={strings.sheet.reminderAdd}
                  value={item.kind}
                  onChange={(e) =>
                    updateCustom(
                      item.id,
                      e.target.value === 'custom_at'
                        ? { kind: 'custom_at', date: form.date, time: '10:00' }
                        : { kind: 'custom_offset', amount: 2, unit: 'hours' },
                    )
                  }
                >
                  <option value="custom_offset">{strings.sheet.reminderKindOffset}</option>
                  <option value="custom_at">{strings.sheet.reminderKindExact}</option>
                </Select>                {item.kind === 'custom_offset' ? (
                  <div className="dl-due-row">
                    <Input
                      type="number"
                      min={1}
                      header={strings.sheet.reminderOffsetValue}
                      value={String(item.amount)}
                      onChange={(e) => updateCustom(item.id, { amount: Number(e.target.value) })}
                    />
                    <Select
                      header={strings.sheet.reminderOffsetUnit}
                      value={item.unit}
                      onChange={(e) => updateCustom(item.id, { unit: e.target.value as ReminderUnit })}
                    >
                      <option value="hours">{strings.sheet.reminderUnitHours}</option>
                      <option value="days">{strings.sheet.reminderUnitDays}</option>
                    </Select>
                  </div>
                ) : (
                  <div className="dl-due-row">
                    <Input
                      type="date"
                      header={strings.sheet.fieldDate}
                      value={item.date}
                      onChange={(e) => updateCustom(item.id, { date: e.target.value })}
                    />
                    <Input
                      type="time"
                      header={strings.sheet.fieldTime}
                      value={item.time}
                      onChange={(e) => updateCustom(item.id, { time: e.target.value })}
                    />
                  </div>
                )}
                <div className="dl-row">
                  <Button size="s" mode="plain" onClick={() => removeCustom(item.id)}>
                    {strings.sheet.reminderRemove}
                  </Button>
                </div>
                {showErrors && validation.errors.custom[item.id] ? (
                  <div className="dl-error" role="alert">
                    {validation.errors.custom[item.id]}
                  </div>
                ) : null}
              </div>
            ))}

            <div className="dl-chips">
              <button
                type="button"
                className="dl-chip dl-chip--button"
                data-testid="add-reminder"
                onClick={() => addCustom('custom_offset')}
              >
                {strings.sheet.reminderAdd}
              </button>
            </div>

            {showErrors && validation.errors.reminders ? (
              <div className="dl-error" role="alert" data-testid="error-reminders">
                {validation.errors.reminders}
              </div>
            ) : null}

            {/* Сводка по кастомным напоминаниям: сами тексты стоят у своих
                элементов, но если их несколько, submit просто «не работает» —
                сводка объясняет, что именно мешает отправке. */}
            {showErrors && validation.reminderErrors ? (
              <div className="dl-error" role="alert" data-testid="error-custom-reminders">
                {strings.sheet.errCustomReminder}
              </div>
            ) : null}
          </>
        )}

        {submitError ? (
          <div className="dl-error" role="alert" data-testid="error-submit">
            {submitError}
          </div>
        ) : null}

        {/* Кнопка внизу шита: в Telegram submit живёт на MainButton, поэтому
            показываем собственную кнопку только вне Telegram (dev/браузер) —
            иначе было бы две конкурирующие кнопки отправки. */}
        {!mainButtonAvailable ? (
          <div className="dl-row">
            <Button
              size="l"
              stretched
              loading={pending}
              disabled={!canSubmit}
              onClick={() => void submit()}
              data-testid="sheet-submit"
            >
              {isEdit ? strings.sheet.submitSave : strings.sheet.submitCreate}
            </Button>
          </div>
        ) : (
          // В Telegram submit живёт на MainButton — своя кнопка не нужна, но
          // подсказка объясняет, почему её нет.
          <div className="dl-hint">{strings.sheet.submitViaMainButton}</div>
        )}

        {isEdit && deadline && canAct ? (
          <>
            <Divider />
            <div className="dl-row">
              <Button
                size="l"
                stretched
                mode="gray"
                disabled={pending}
                onClick={() => void doComplete()}
                data-testid="sheet-complete"
              >
                {strings.sheet.actionComplete}
              </Button>
            </div>
            <div className="dl-row">
              <Button
                size="l"
                stretched
                mode="plain"
                className="dl-danger"
                disabled={pending}
                onClick={() => setConfirmDelete(true)}
                data-testid="sheet-delete"
              >
                {strings.sheet.actionDelete}
              </Button>
            </div>
            <Caption level="1" className="dl-hint">
              {tpl(strings.sheet.metaCreated, formatDue(deadline.created_at, tz))}
            </Caption>
          </>
        ) : null}
      </div>

      <ConfirmDialog
        open={confirmDelete}
        title={strings.sheet.confirmDelete}
        description={strings.sheet.confirmDeleteHint}
        confirmLabel={strings.sheet.actionDelete}
        confirmTestId="confirm-delete"
        nested
        onConfirm={() => void doDelete()}
        onCancel={() => setConfirmDelete(false)}
      />
    </Modal>
  );
}

interface SheetHeaderProps {
  title: string;
  onClose: () => void;
}

/**
 * Заголовок шита. Modal.Header кита рендерит текст только на iOS (на 'base'
 * остаётся пустой блок), поэтому заголовок собираем сами.
 */
function SheetHeader({ title, onClose }: SheetHeaderProps) {
  return (
    <div className="dl-sheet-header">
      <span className="dl-sheet-header__title">{title}</span>
      <button
        type="button"
        className="dl-sheet-header__close"
        aria-label={strings.common.close}
        onClick={onClose}
      >
        ✕
      </button>
    </div>
  );
}

/** Заголовок блока внутри шита (Section рисует фон списка — в модалке лишний). */
function Sectionish({ header, children }: { header: string; children: ReactNode }) {
  return (
    <div className="dl-section-block">
      <div className="dl-section-header">{header}</div>
      {children}
    </div>
  );
}

/** Подпись статуса напоминания. */
export function statusLabel(status: Reminder['status']): string {
  switch (status) {
    case 'pending':
      return strings.sheet.reminderStatusPending;
    case 'sent':
      return strings.sheet.reminderStatusSent;
    case 'cancelled':
      return strings.sheet.reminderStatusCancelled;
    default:
      return strings.sheet.reminderStatusFailed;
  }
}