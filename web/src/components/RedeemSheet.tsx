// Шит «Ввести инвайт-код» (спека §3.2): код от админа группы → membership.
//
// Код приводится к верхнему регистру: сервер нормализует его сам
// (RedeemInvite: ToUpper+Trim), и клиент не должен требовать от пользователя
// аккуратности с регистром. Ошибки (неизвестный/отозванный/истёкший — все
// неразличимы как 404, исчерпанный max_uses — 409, лимиты — 429) показываются
// строкой под полем.
import { useEffect, useState } from 'react';
import { Button, Input, Modal } from './ui';
import { SheetHeader } from './SheetHeader';
import { useRedeemInvite } from '../lib/queries';
import { redeemErrorMessage } from '../lib/errorText';
import { strings } from '../lib/strings';

interface RedeemSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Успех: группа, в которую вступили. */
  onRedeemed?: (groupID: number, slug: string) => void;
}

export function RedeemSheet({ open, onOpenChange, onRedeemed }: RedeemSheetProps) {
  const redeem = useRedeemInvite();
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [attempted, setAttempted] = useState(false);

  useEffect(() => {
    if (!open) return;
    setCode('');
    setError(null);
    setAttempted(false);
  }, [open]);

  const normalized = code.trim().toUpperCase();
  const empty = normalized === '';

  async function submit() {
    setAttempted(true);
    setError(null);
    if (empty) return;
    try {
      const { group } = await redeem.mutateAsync(normalized);
      onOpenChange(false);
      onRedeemed?.(group.id, group.slug);
    } catch (e) {
      setError(redeemErrorMessage(e));
    }
  }

  return (
    <Modal open={open} onOpenChange={onOpenChange} header={<SheetHeader title={strings.groups.redeemTitle} onClose={() => onOpenChange(false)} />}>
      <div className="dl-sheet" data-testid="redeem-sheet">
        <Input
          header={strings.groups.fieldCode}
          placeholder={strings.groups.fieldCodePlaceholder}
          value={code}
          maxLength={16}
          status={attempted && empty ? 'error' : 'default'}
          onChange={(e) => setCode(e.target.value)}
          onBlur={() => setAttempted(true)}
          data-testid="field-invite-code"
        />
        <div className="dl-hint">{strings.groups.redeemHint}</div>

        {error ? (
          <div className="dl-error" role="alert" data-testid="redeem-error">
            {error}
          </div>
        ) : null}

        <div className="dl-row">
          <Button
            size="l"
            stretched
            loading={redeem.isPending}
            disabled={redeem.isPending || empty}
            data-testid="submit-redeem"
            onClick={() => void submit()}
          >
            {strings.groups.redeemSubmit}
          </Button>
        </div>
      </div>
    </Modal>
  );
}