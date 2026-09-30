// Шит claim-флоу «Стать админом» (спека §3.1): start → код в чат → ввод →
// подтверждение → роль admin.
//
// Вся логика переходов живёт в lib/claim.ts (чистые функции, покрыты
// табличными тестами); здесь только рендер состояния и вызовы API. Это
// принципиально: ветвление по ошибкам сервера (409 нет привязки, 429 лимит,
// 403 неверный код, 404 истёк) — единственное место, где UI может соврать
// пользователю, и оно должно быть проверяемо без DOM.
//
// Отдельная тонкость: шит открывается сразу в состоянии no_binding, если
// родитель уже знает, что чат не привязан (binding === null в деталях группы):
// пользователь видит инструкцию /bind_group, не тратя запрос на 409.
import { useEffect, useMemo, useState } from 'react';
import { Button, Cell, Input, Modal } from '@telegram-apps/telegram-ui';
import { SheetHeader } from './SheetHeader';
import { ConfirmDialog } from './ConfirmDialog';
import { useConfirmClaim, useStartClaim } from '../lib/queries';
import {
  initialClaimState,
  isValidClaimCode,
  claimRemainingMs,
  onConfirmFailed,
  onConfirmRequested,
  onClaimConfirmed,
  onStartFailed,
  onStartRequested,
  onStarted,
  type ClaimModel,
} from '../lib/claim';
import { claimErrorMessage, rateLimitMessage } from '../lib/errorText';
import { humanDuration } from '../lib/format';
import { strings, tpl } from '../lib/strings';
import { hapticImpact, hapticNotification } from '../lib/tma';

/** Обновление подписи «код действует ещё N» раз в секунду. */
function useSecondTick(active: boolean): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    if (!active) return;
    const id = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(id);
  }, [active]);
  return now;
}

interface ClaimSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  groupID: number;
  /** Слаг группы — для инструкции /bind_group. */
  slug: string;
  /** Привязан ли чат (из деталей группы): false → сразу no_binding. */
  hasBinding: boolean;
  /** Роль выдана: родитель перезапрашивает детали и обновляет экран. */
  onConfirmed: () => void;
}

export function ClaimSheet({
  open,
  onOpenChange,
  groupID,
  slug,
  hasBinding,
  onConfirmed,
}: ClaimSheetProps) {
  const start = useStartClaim();
  const confirm = useConfirmClaim();
  const [model, setModel] = useState<ClaimModel>(() => initialClaimState(hasBinding));
  const [code, setCode] = useState('');
  const [formatError, setFormatError] = useState<string | null>(null);

  const ticking = model.state === 'code_sent' && model.session !== null;
  const now = useSecondTick(ticking);

  // Открытие — чистое состояние от актуального знания о привязке: иначе после
  // успешного claim шит во второй раз показывал бы «код отправлен».
  useEffect(() => {
    if (!open) return;
    setModel(initialClaimState(hasBinding));
    setCode('');
    setFormatError(null);
    // hasBinding перечитывать при каждом рендере не нужно: значение фиксируется
    // в момент открытия шита, а дальше состояние ведёт сам флоу.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const messages = useMemo(
    () => ({
      noBinding: strings.groups.errClaimNoBinding,
      sendFailed: strings.groups.errClaimSendFailed,
      badCode: strings.groups.errClaimBadCode,
      expired: strings.groups.errClaimCodeExpired,
      rateLimited: (ms: number | null) => rateLimitMessage(ms),
      generic: (err: unknown) => claimErrorMessage(err),
    }),
    [],
  );

  async function doStart() {
    setModel((m) => onStartRequested(m));
    setFormatError(null);
    try {
      const started = await start.mutateAsync(groupID);
      hapticImpact('medium');
      setModel((m) => onStarted(m, { expiresAt: started.expires_at, chatId: started.chat_id }));
    } catch (e) {
      setModel((m) => onStartFailed(m, e, messages));
    }
  }

  async function doConfirm() {
    const candidate = code.trim();
    if (!isValidClaimCode(candidate)) {
      setFormatError(strings.groups.errClaimFormat);
      hapticImpact('rigid');
      return;
    }
    setFormatError(null);
    setModel((m) => onConfirmRequested(m));
    try {
      await confirm.mutateAsync({ groupID, code: candidate });
      setModel((m) => onClaimConfirmed(m));
      hapticNotification('success');
      onConfirmed();
      onOpenChange(false);
    } catch (e) {
      setModel((m) => onConfirmFailed(m, e, messages));
    }
  }

  const remaining = model.session ? claimRemainingMs(model.session.expiresAt, now) : 0;
  const expired = model.state === 'code_sent' && model.session !== null && remaining <= 0;
  const busy = model.state === 'starting' || model.state === 'confirming';
  const error = formatError ?? model.error;

  return (
    <Modal open={open} onOpenChange={onOpenChange} header={<SheetHeader title={strings.groups.claimTitle} onClose={() => onOpenChange(false)} />}>
      <div className="dl-sheet" data-testid="claim-sheet" data-state={model.state}>
        {model.state === 'no_binding' ? (
          <>
            <div className="dl-section-header">{strings.groups.claimNoBinding}</div>
            <div className="dl-hint" data-testid="claim-no-binding-hint">
              {tpl(strings.groups.claimNoBindingHint, slug)}
            </div>
            <div className="dl-row">
              <Button size="l" stretched mode="gray" onClick={() => onOpenChange(false)}>
                {strings.common.close}
              </Button>
            </div>
          </>
        ) : (
          <>
            <div className="dl-hint">{strings.groups.claimHint}</div>

            {model.state === 'idle' ? (
              <div className="dl-row">
                <Button
                  size="l"
                  stretched
                  loading={busy}
                  data-testid="claim-start"
                  onClick={() => void doStart()}
                >
                  {strings.groups.claimSend}
                </Button>
              </div>
            ) : null}

            {model.state === 'starting' ? (
              <div className="dl-row">
                <Button size="l" stretched loading disabled>
                  {strings.groups.claimSend}
                </Button>
              </div>
            ) : null}

            {ticking || expired || model.state === 'confirming' ? (
              <>
                <div className="dl-section-header">
                  {expired ? strings.groups.claimExpired : strings.groups.claimCodeSent}
                </div>
                {!expired ? (
                  <div className="dl-hint" data-testid="claim-expires">
                    {tpl(strings.groups.claimExpiresIn, humanDuration(Math.max(0, remaining)))}
                  </div>
                ) : null}
                <Cell multiline>
                  <Input
                    header={strings.groups.claimCodeLabel}
                    placeholder={strings.groups.claimCodePlaceholder}
                    value={code}
                    inputMode="numeric"
                    maxLength={6}
                    disabled={busy}
                    onChange={(e) => setCode(e.target.value.replace(/[^0-9]/g, ''))}
                    data-testid="claim-code"
                  />
                </Cell>
                <div className="dl-hint">{strings.groups.claimCodeSentHint}</div>
                <div className="dl-row">
                  <Button
                    size="l"
                    stretched
                    loading={model.state === 'confirming'}
                    disabled={model.state === 'confirming' || code.trim().length !== 6}
                    data-testid="claim-confirm"
                    onClick={() => void doConfirm()}
                  >
                    {strings.groups.claimConfirm}
                  </Button>
                </div>
                {expired ? (
                  <div className="dl-row">
                    <Button
                      size="l"
                      stretched
                      mode="gray"
                      data-testid="claim-restart"
                      onClick={() => void doStart()}
                    >
                      {strings.groups.claimSend}
                    </Button>
                  </div>
                ) : null}
              </>
            ) : null}

            {error ? (
              <div className="dl-error" role="alert" data-testid="claim-error">
                {error}
              </div>
            ) : null}
          </>
        )}
      </div>
    </Modal>
  );
}

/**
 * Диалог подтверждения отзыва claim-кода: действие гасит код, который уже
 * видел весь чат, поэтому спрашиваем явно (спека §3.1 — «админы получают
 * уведомление и могут отозвать код»).
 */
export function RevokeClaimDialog({
  open,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <ConfirmDialog
      open={open}
      title={strings.groups.claimRevokeConfirm}
      description={strings.groups.claimRevokeConfirmHint}
      confirmLabel={strings.groups.claimRevoke}
      confirmTestId="confirm-claim-revoke"
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  );
}