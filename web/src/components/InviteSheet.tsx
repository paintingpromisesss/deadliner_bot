// Шит «Новый инвайт» (спека §3.2): роль, число использований, срок жизни и
// публикация в чат. Plaintext-код сервер возвращает РОВНО ОДИН РАЗ (в БД —
// SHA-256), поэтому шит после создания не закрывается, а показывает код с
// кнопкой копирования: закрытие по успеху лишило бы шанса скопировать.
import { useEffect, useState } from 'react';
import { Button, Input, Modal, Select } from './ui';
import { SheetHeader } from './SheetHeader';
import { useCreateInvite } from '../lib/queries';
import { invitePublishErrorMessage } from '../lib/errorText';
import { strings, tpl } from '../lib/strings';
import { formatDue } from '../lib/format';
import type { InviteCreated } from '../lib/groups';
import { hapticImpact } from '../lib/tma';

/**
 * Варианты TTL из UI. Значение уходит в ttl_hours как есть:
 *  -1 — бессрочный (no expiration: действует, пока не отозван и не исчерпан
 *      лимит активаций);
 *  0 — не используется (неоднозначен);
 *  пресеты 24/168/720/2160 + «кастомные» часы (максимум 2160 = 90 дней).
 */
const TTL_OPTIONS = [
  { value: -1, label: '' },
  { value: 24, label: '' },
  { value: 168, label: '' },
  { value: 720, label: '' },
  { value: 2160, label: '' },
  { value: -2, label: '' }, // сигнальное: «свой срок»
] as const;

function ttlLabel(hours: number): string {
  switch (hours) {
    case -1:
      return strings.groups.inviteTTLPersistent;
    case 24:
      return strings.groups.inviteTTL1;
    case 168:
      return strings.groups.inviteTTL7;
    case 720:
      return strings.groups.inviteTTL30;
    case 2160:
      return strings.groups.inviteTTL90;
    case -2:
      return strings.groups.inviteTTLCustom;
    default:
      return tpl(strings.groups.inviteTTLHours, hours);
  }
}

interface InviteSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  groupID: number;
  tz: string;
  /** Привязан ли чат группы: публикация в чат без привязки невозможна. */
  hasBinding: boolean;
  /** Созданный код — родитель хранит его в сессионном списке. Роль передаём
   * вместе с кодом: иначе подпись списка всегда врала бы про «участника». */
  onCreated?: (invite: InviteCreated, role: 'admin' | 'member') => void;
}

export function InviteSheet({ open, onOpenChange, groupID, tz, hasBinding, onCreated }: InviteSheetProps) {
  const create = useCreateInvite();
  const [role, setRole] = useState<'admin' | 'member'>('member');
  const [unlimited, setUnlimited] = useState(true);
  const [maxUses, setMaxUses] = useState('10');
  const [ttlHours, setTTLHours] = useState<number>(168);
  const [customHours, setCustomHours] = useState('48');
  const [publish, setPublish] = useState(false);
  const [created, setCreated] = useState<InviteCreated | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!open) return;
    setRole('member');
    setUnlimited(true);
    setMaxUses('10');
    setTTLHours(168);
    setCustomHours('48');
    setPublish(false);
    setCreated(null);
    setError(null);
    setCopied(false);
  }, [open]);

  const uses = unlimited ? -1 : Number(maxUses);
  const usesValid = unlimited || (Number.isInteger(uses) && uses >= 1);
  const custom = ttlHours === -2;
  const customValue = Number(customHours);
  // Часы — целые, 1..2160 (90 дней), как maxTTLHours контроллера.
  const customValid = !custom || (Number.isInteger(customValue) && customValue >= 1 && customValue <= 2160);
  const effectiveTTL = custom ? customValue : ttlHours;

  async function submit() {
    setError(null);
    if (!usesValid) {
      setError(tpl(strings.groups.inviteMaxUsesLimit, 1));
      return;
    }
    if (!customValid) {
      setError(tpl(strings.groups.inviteTTLBounds, 2160));
      return;
    }
    try {
      const invite = await create.mutateAsync({
        groupID,
        input: {
          role,
          max_uses: uses,
          ttl_hours: effectiveTTL,
          publish_to_chat: publish && hasBinding,
        },
      });
      setCreated(invite);
      onCreated?.(invite, role);
    } catch (e) {
      setError(invitePublishErrorMessage(e));
    }
  }

  async function copy() {
    if (!created) return;
    hapticImpact('light');
    try {
      await navigator.clipboard.writeText(created.code);
      setCopied(true);
    } catch {
      // Буфер обмена недоступен (нет разрешения/не HTTPS): код остаётся на
      // экране и его можно выделить руками — молчаливая неудача копирования
      // не должна выглядеть как успех.
      setCopied(false);
    }
  }

  return (
    <Modal open={open} onOpenChange={onOpenChange} header={<SheetHeader title={strings.groups.inviteTitle} onClose={() => onOpenChange(false)} />}>
      <div className="dl-sheet" data-testid="invite-sheet">
        {created ? (
          <>
            <div className="dl-section-header">{strings.groups.inviteCreated}</div>
            <div className="dl-row" data-testid="invite-code-value">
              <span className="dl-code">{created.code}</span>
            </div>
            <div className="dl-hint" data-testid="invite-expires">
              {created.expires_at
                ? tpl(strings.groups.inviteExpiresAt, formatDue(created.expires_at, tz))
                : strings.groups.inviteExpiresNever}
            </div>
            {created.published ? (
              <div className="dl-hint" data-testid="invite-published">
                {strings.groups.invitePublished}
              </div>
            ) : null}
            <div className="dl-row">
              <Button size="l" stretched mode="gray" data-testid="invite-copy" onClick={() => void copy()}>
                {copied ? strings.groups.inviteCopied : strings.groups.inviteCopy}
              </Button>
            </div>
          </>
        ) : (
          <>
            <Select
              header={strings.groups.inviteRole}
              value={role}
              onChange={(e) => setRole(e.target.value === 'admin' ? 'admin' : 'member')}
              data-testid="invite-role"
            >
              <option value="member">{strings.groups.inviteRoleMember}</option>
              <option value="admin">{strings.groups.inviteRoleAdmin}</option>
            </Select>

            <label className="dl-cell dl-cell--multiline">
              <span className="dl-cell__main">
                <span className="dl-cell__title">{strings.groups.inviteMaxUsesUnlimited}</span>
              </span>
              <span className="dl-cell__after">
                <input
                  type="checkbox"
                  checked={unlimited}
                  data-testid="invite-unlimited"
                  onChange={(e) => setUnlimited(e.target.checked)}
                />
              </span>
            </label>
            {!unlimited ? (
              <Input
                header={strings.groups.inviteMaxUses}
                value={maxUses}
                inputMode="numeric"
                onChange={(e) => setMaxUses(e.target.value.replace(/[^0-9]/g, ''))}
                data-testid="invite-max-uses"
              />
            ) : null}

            <Select
              header={strings.groups.inviteTTL}
              value={String(ttlHours)}
              onChange={(e) => setTTLHours(Number(e.target.value))}
              data-testid="invite-ttl"
            >
              {TTL_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {ttlLabel(opt.value)}
                </option>
              ))}
            </Select>
            {custom ? (
              <Input
                header={strings.groups.inviteTTLHoursLabel}
                value={customHours}
                inputMode="numeric"
                placeholder="48"
                onChange={(e) => setCustomHours(e.target.value.replace(/[^0-9]/g, ''))}
                data-testid="invite-ttl-custom"
              />
            ) : null}

            <label className="dl-cell dl-cell--multiline">
              <span className="dl-cell__main">
                <span className="dl-cell__title">{strings.groups.invitePublish}</span>
                <span className="dl-cell__subtitle">
                  {hasBinding ? strings.groups.invitePublishHint : strings.groups.invitePublishNoBinding}
                </span>
              </span>
              <span className="dl-cell__after">
                <input
                  type="checkbox"
                  checked={publish && hasBinding}
                  disabled={!hasBinding}
                  data-testid="invite-publish"
                  onChange={(e) => {
                    hapticImpact('light');
                    setPublish(e.target.checked);
                  }}
                />
              </span>
            </label>

            {error ? (
              <div className="dl-error" role="alert" data-testid="invite-error">
                {error}
              </div>
            ) : null}

            <div className="dl-hint">{strings.groups.invitesHint}</div>

            <div className="dl-row">
              <Button
                size="l"
                stretched
                loading={create.isPending}
                disabled={create.isPending}
                data-testid="submit-invite"
                onClick={() => void submit()}
              >
                {strings.groups.inviteCreate}
              </Button>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

/** Подпись строки сессионного кода: «участник · до 05.10.2026 12:00». */
export function inviteSessionSubtitle(
  invite: InviteCreated,
  role: 'admin' | 'member',
  tz: string,
): string {
  const roleText = role === 'admin' ? strings.groups.inviteRoleAdmin : strings.groups.inviteRoleMember;
  const expiry = invite.expires_at
    ? tpl(strings.groups.inviteExpiresAt, formatDue(invite.expires_at, tz))
    : strings.groups.inviteExpiresNever;
  return `${roleText} · ${expiry}`;
}
