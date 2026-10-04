// Экран подтверждения вступления по инвайт-параметру (Main App flow):
// бот публикует в чат группы приглашение с inline-кнопкой, открывающей
// Mini App со startapp = код инвайта. Здесь показываем «Вступить в группу
// [Название]?» с кнопками «Вступить» и «Отмена».
//
// Лимит инвайта расходуется ТОЛЬКО кнопкой «Вступить» (RedeemInvite на
// бэкенде — атомарный IncrementUsed): простое открытие Mini App или «Отмена»
// ничего не списывает.
import { useEffect, useState } from 'react';
import { Button, Modal, Spinner } from './ui';
import { SheetHeader } from './SheetHeader';
import { useInvitePreview, useRedeemInvite } from '../lib/queries';
import { redeemErrorMessage } from '../lib/errorText';
import { strings } from '../lib/strings';
import { hapticNotification } from '../lib/tma';
import type { Group } from '../lib/groups';

interface JoinGroupSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Код инвайта из startapp-параметра. */
  code: string;
  /** Успех: вызывающий закрывает шит и уходит в группу. */
  onJoined?: (group: Group) => void;
}

export function JoinGroupSheet({ open, onOpenChange, code, onJoined }: JoinGroupSheetProps) {
  const preview = useInvitePreview(code, open);
  const redeem = useRedeemInvite();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setBusy(false);
  }, [open, code]);

  async function join() {
    setBusy(true);
    setError(null);
    try {
      const { group } = await redeem.mutateAsync(code.trim().toUpperCase());
      onOpenChange(false);
      onJoined?.(group);
    } catch (e) {
      setError(redeemErrorMessage(e));
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        // «Отмена»/закрытие окна: лимит инвайта не расходуется.
        if (!next) hapticNotification('warning');
        onOpenChange(next);
      }}
      header={<SheetHeader title={strings.join.title} onClose={() => onOpenChange(false)} />}
    >
      <div className="dl-sheet" data-testid="join-group-sheet">
        {preview.isLoading ? (
          <div className="dl-centered">
            <Spinner size="m" />
          </div>
        ) : preview.isError || !preview.data ? (
          <>
            <div className="dl-error" role="alert" data-testid="join-preview-error">
              {preview.error instanceof Error ? preview.error.message : strings.groups.errCodeUnknown}
            </div>
            <div className="dl-row">
              <Button size="l" stretched mode="gray" onClick={() => onOpenChange(false)}>
                {strings.common.close}
              </Button>
            </div>
          </>
        ) : (
          <>
            <div className="dl-section-header" data-testid="join-group-name">
              {preview.data.group.title || preview.data.group.slug}
            </div>
            <div className="dl-hint">{strings.join.hint}</div>
            {error ? (
              <div className="dl-error" role="alert" data-testid="join-error">
                {error}
              </div>
            ) : null}
            <div className="dl-row">
              <Button
                size="l"
                stretched
                loading={busy}
                disabled={busy}
                data-testid="join-confirm"
                onClick={() => void join()}
              >
                {strings.join.confirm}
              </Button>
            </div>
            <div className="dl-row">
              <Button
                size="l"
                stretched
                mode="gray"
                disabled={busy}
                data-testid="join-cancel"
                onClick={() => onOpenChange(false)}
              >
                {strings.join.cancel}
              </Button>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}
