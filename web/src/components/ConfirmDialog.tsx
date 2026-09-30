// Подтверждение необратимого действия (удаление дедлайна). Отдельный
// компонент, потому что подтверждение нужно в двух местах: из списка (кнопка
// удаления на строке) и из формы редактирования. Собственная кнопка/заголовок —
// Modal.Header кита рендерит текст только на iOS.
import { Button, Modal } from '@telegram-apps/telegram-ui';
import { strings } from '../lib/strings';
import { hapticNotification } from '../lib/tma';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description?: string;
  /** Текст подтверждающей кнопки (destructive-вид). */
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  /** true, если диалог открывается поверх другого Modal (vaul). */
  nested?: boolean;
  /** data-testid подтверждающей кнопки. */
  confirmTestId?: string;
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  onConfirm,
  onCancel,
  nested = false,
  confirmTestId,
}: ConfirmDialogProps) {
  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        if (!next) onCancel();
      }}
      nested={nested}
      header={
        <div className="dl-sheet-header">
          <span className="dl-sheet-header__title">{title}</span>
          <button
            type="button"
            className="dl-sheet-header__close"
            aria-label={strings.common.close}
            onClick={onCancel}
          >
            ✕
          </button>
        </div>
      }
    >
      <div className="dl-sheet">
        {description ? <div className="dl-hint">{description}</div> : null}
        <div className="dl-row">
          <Button
            size="l"
            stretched
            mode="plain"
            className="dl-danger"
            data-testid={confirmTestId}
            onClick={() => {
              hapticNotification('warning');
              onConfirm();
            }}
          >
            {confirmLabel}
          </Button>
        </div>
        <div className="dl-row">
          <Button size="l" stretched mode="gray" onClick={onCancel}>
            {strings.common.cancel}
          </Button>
        </div>
      </div>
    </Modal>
  );
}