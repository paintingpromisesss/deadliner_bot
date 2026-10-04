// Экран группы (спека §9, экраны 5 и 7): шапка, привязка чата, участники,
// роли и админ-панель. Гейт по роли — не украшение: участнику backend отдаст
// 403 на изменяющие вызовы (requireAdmin), поэтому админ-панель ему не
// рендерится, а его единственная операция «Выйти» вынесена отдельно. Список
// инвайтов — состояние сессии: plaintext-код API отдаёт только при создании
// (в БД SHA-256, GET-списка кодов нет), показать можно лишь созданное здесь.
import { useEffect, useState } from 'react';
import { Button, Cell, List, Placeholder, Section, Spinner } from '../../components/ui';
import { Screen } from '../../components/Screen';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { InviteSheet } from '../../components/InviteSheet';
import { MemberCell, memberName } from '../../components/MemberCell';
import { RoleBadge, roleLabel } from '../../components/RoleBadge';
import { SlugAvatar } from '../../components/SlugAvatar';
import { navigate } from '../../router';
import { useAuthStore } from '../../stores/auth';
import {
  usePendingGroupDeadlines,
  useDeleteGroup,
  useGroupDetail,
  useGroupInvites,
  useGroupMembers,
  useKickMember,
  useLeaveGroup,
  useRevokeInvite,
  useSetMemberRole,
  useApproveDeadline,
  useRejectDeadline,
} from '../../lib/queries';
import { memberActionErrorMessage, mutationErrorMessage } from '../../lib/errorText';
import { formatDue } from '../../lib/format';
import { strings, tpl } from '../../lib/strings';
import { hapticImpact, hapticNotification } from '../../lib/tma';
import type { GroupInvite, InviteCreated, Member } from '../../lib/groups';
import type { Deadline } from '../../lib/deadlines';

/** Статус группы в шапке. */
function statusText(status: string): string {
  switch (status) {
    case 'pending':
      return strings.groups.statusPending;
    case 'archived':
      return strings.groups.statusArchived;
    default:
      return strings.groups.statusActive;
  }
}

function IconCopy({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </svg>
  );
}

function IconClose({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
  );
}

function IconCheck({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

interface GroupDetailScreenProps {
  groupID: number;
}

export function GroupDetailScreen({ groupID }: GroupDetailScreenProps) {
  const tz = useAuthStore((s) => s.user?.tz ?? 'Europe/Moscow');
  const me = useAuthStore((s) => s.user?.id ?? null);

  const detail = useGroupDetail(groupID);
  const isAdmin = detail.data?.role === 'admin';
  // Участники нужны и участнику (счётчики/состав), но тянуть их отдельным
  // запросом раньше роли — лишний 403: ждём детали и грузим только когда роль
  // известна и это либо админ, либо член группы.
  const canReadMembers = detail.data != null && detail.data.role !== '';
  const members = useGroupMembers(groupID, canReadMembers);
  // Модерация: дедлайны группы в pending_approval видит только админ.
  const pending = usePendingGroupDeadlines(groupID, isAdmin);
  // Сохранённые инвайты группы (админ).
  const invites = useGroupInvites(groupID, isAdmin);
  const activeInvites = (invites.data?.invites ?? []).filter(
    (inv) => inv.status !== 'revoked' && !inv.revoked_at,
  );

  const leave = useLeaveGroup();
  const removeGroup = useDeleteGroup();
  const setRole = useSetMemberRole();
  const kick = useKickMember();
  const revokeInvite = useRevokeInvite();
  const approveDeadline = useApproveDeadline();
  const rejectDeadline = useRejectDeadline();

  const [actionError, setActionError] = useState<string | null>(null);
  const [menuFor, setMenuFor] = useState<number | null>(null);
  const [pendingKick, setPendingKick] = useState<Member | null>(null);
  const [confirmLeave, setConfirmLeave] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmRevoke, setConfirmRevoke] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<{ code: string } | null>(null);
  const [inviteOpen, setInviteOpen] = useState(false);
  // Дедлайн на модерации, который подтверждаем/отклоняем.
  const [moderationTarget, setModerationTarget] = useState<Deadline | null>(null);
  const [moderationAction, setModerationAction] = useState<'approve' | 'reject'>('approve');
  const [notice, setNotice] = useState<string | null>(null);
  const [copiedCode, setCopiedCode] = useState<string | null>(null);

  // Смена группы (переход из списка в другую) — чистое состояние.
  useEffect(() => {
    setActionError(null);
    setMenuFor(null);
    setNotice(null);
    setCopiedCode(null);
  }, [groupID]);

  const expiresAt = detail.data?.group.claim_expires_at ?? null;

  async function run(fn: () => Promise<unknown>, mapError = memberActionErrorMessage) {
    setActionError(null);
    try {
      await fn();
    } catch (e) {
      hapticNotification('error');
      setActionError(mapError(e));
    }
  }

  const memberList: Member[] = members.data ?? [];
  const pendingDeadlines: Deadline[] = pending.data ?? [];

  if (detail.isLoading) {
    return (
      <Screen title={strings.groups.detailTitle}>
        <div className="dl-centered">
          <Spinner size="m" />
        </div>
      </Screen>
    );
  }

  if (detail.isError || !detail.data) {
    return (
      <Screen title={strings.groups.detailTitle}>
        <Placeholder
          header={strings.common.loadError}
          description={detail.error instanceof Error ? detail.error.message : undefined}
          action={
            <Button size="m" onClick={() => void detail.refetch()}>
              {strings.common.retry}
            </Button>
          }
        />
      </Screen>
    );
  }

  const { group, role, binding, members_count } = detail.data;
  const inGroup = role !== '';

  return (
    <Screen title={group.title || group.slug}>
      <List>
        <Section>
          <Cell
            Component="button"
            type="button"
            className="dl-cell-button"
            data-testid="back-to-groups"
            onClick={() => navigate('/groups')}
          >
            {strings.groups.backToList}
          </Cell>
        </Section>

        <Section data-testid="group-header">
          <Cell
            before={<SlugAvatar slug={group.slug} />}
            subtitle={tpl(strings.groups.membersCount, members_count)}
            after={<RoleBadge role={role} />}
            multiline
          >
            {group.slug}
          </Cell>
          <Cell
            subtitle={statusText(group.status)}
            data-testid="group-status"
            multiline
          >
            {strings.groups.myRole}: {roleLabel(role)}
          </Cell>
          {group.status === 'pending' && expiresAt ? (
            <Cell multiline subtitle={tpl(strings.groups.pendingUntil, formatDue(expiresAt, tz))}>
              {strings.groups.statusPending}
            </Cell>
          ) : null}
        </Section>

        <Section header={strings.groups.bindingHeader} data-testid="binding-section">
          {binding ? (
            <Cell
              multiline
              data-testid="binding-status"
              subtitle={
                binding.message_thread_id != null
                  ? tpl(
                      strings.groups.bindingTopic,
                      binding.chat_title,
                      binding.topic_name || binding.message_thread_id,
                    )
                  : tpl(strings.groups.bindingChat, binding.chat_title)
              }
            >
              {strings.groups.bindingHeader}
            </Cell>
          ) : (
            <>
              <Cell
                multiline
                data-testid="binding-status"
                subtitle={tpl(strings.groups.bindingHint, group.slug)}
              >
                {strings.groups.bindingNone}
              </Cell>
            </>
          )}
        </Section>

        {actionError ? (
          <div className="dl-error" role="alert" data-testid="action-error">
            {actionError}
          </div>
        ) : null}

        {notice ? (
          <div className="dl-hint" role="status" data-testid="detail-notice">
            {notice}
          </div>
        ) : null}

        {isAdmin ? (
          <>
            <Section header={strings.groups.membersHeader} data-testid="members-section">
              {members.isLoading ? (
                <div className="dl-centered">
                  <Spinner size="s" />
                </div>
              ) : members.isError ? (
                <div className="dl-error" role="alert">
                  {strings.common.loadError}
                </div>
              ) : memberList.length === 0 ? (
                <div className="dl-hint">{strings.groups.membersEmpty}</div>
              ) : (
                memberList.map((member) => (
                  <MemberCell
                    key={member.user_id}
                    member={member}
                    isMe={member.user_id === me}
                    canManage
                    menuOpen={menuFor === member.user_id}
                    onMenuToggle={() =>
                      setMenuFor((cur) => (cur === member.user_id ? null : member.user_id))
                    }
                    onPromote={(m) =>
                      void run(() =>
                        setRole.mutateAsync({ groupID, userID: m.user_id, role: 'admin' }),
                      )
                    }
                    onDemote={(m) =>
                      void run(() =>
                        setRole.mutateAsync({ groupID, userID: m.user_id, role: 'member' }),
                      )
                    }
                    onKick={setPendingKick}
                    tz={tz}
                  />
                ))
              )}
            </Section>

            <Section header={strings.groups.invitesHeader} data-testid="invites-section">
              <Cell
                Component="button"
                type="button"
                className="dl-cell-button"
                data-testid="open-invite"
                onClick={() => {
                  hapticImpact('light');
                  setInviteOpen(true);
                }}
              >
                {strings.groups.inviteCreate}
              </Cell>
              {invites.isLoading ? (
                <div className="dl-centered">
                  <Spinner size="s" />
                </div>
              ) : activeInvites.length === 0 ? (
                <div className="dl-cell-empty" data-testid="invites-empty">
                  {strings.groups.inviteEmpty}
                </div>
              ) : (
                activeInvites.map((inv) => (
                  <Cell
                    key={inv.id}
                    multiline
                    data-testid={`group-invite-${inv.code}`}
                    subtitle={
                      `${inv.role === 'admin' ? strings.groups.inviteRoleAdmin : strings.groups.inviteRoleMember} · ` +
                      `${inv.status === 'active' ? strings.groups.inviteStatusActive : inv.status === 'expired' ? strings.groups.inviteStatusExpired : strings.groups.inviteStatusExhausted} · ` +
                      `${inv.max_uses === -1 ? strings.groups.inviteUsesUnlimited : tpl(strings.groups.inviteUsesRemaining, Math.max(0, inv.max_uses - inv.used_count), inv.max_uses)} · ` +
                      `${inv.expires_at ? tpl(strings.groups.inviteExpiresAt, formatDue(inv.expires_at, tz)) : strings.groups.inviteExpiresNever}`
                    }
                    after={
                      <div style={{ display: 'flex', gap: '4px', alignItems: 'center' }}>
                        <button
                          type="button"
                          className={`dl-action dl-action--icon ${copiedCode === inv.code ? 'dl-action--copied' : ''}`}
                          aria-label={copiedCode === inv.code ? strings.groups.inviteCopied : strings.groups.inviteCopy}
                          data-testid={`copy-invite-${inv.code}`}
                          onClick={async () => {
                            hapticImpact('light');
                            try {
                              await navigator.clipboard.writeText(inv.code);
                              setCopiedCode(inv.code);
                              setTimeout(() => {
                                setCopiedCode((cur) => (cur === inv.code ? null : cur));
                              }, 2000);
                            } catch {}
                          }}
                        >
                          {copiedCode === inv.code ? <IconCheck /> : <IconCopy />}
                        </button>
                        {inv.status === 'active' ? (
                          <button
                            type="button"
                            className="dl-action dl-action--icon"
                            aria-label={strings.groups.inviteRevoke}
                            data-testid={`revoke-invite-${inv.code}`}
                            onClick={() => {
                              setRevokeTarget(inv);
                              setConfirmRevoke(true);
                            }}
                          >
                            <IconClose />
                          </button>
                        ) : null}
                      </div>
                    }
                  >
                    <span className="dl-code">{inv.code}</span>
                  </Cell>
                ))
              )}
            </Section>

            {/* Модерация дедлайнов: заявки участников (pending_approval).
                Пустая секция не рендерится вовсе — админ без заявок не
                должен видеть «пустую модерацию». */}
            {pendingDeadlines.length > 0 ? (
              <Section header={strings.groups.moderationHeader} data-testid="moderation-section">
                {pendingDeadlines.map((d) => (
                  <Cell
                    key={d.id}
                    multiline
                    data-testid={`pending-deadline-${d.id}`}
                    subtitle={tpl(strings.groups.moderationDue, formatDue(d.due_at, tz))}
                    after={
                      <span className="dl-row dl-row--tight">
                        <button
                          type="button"
                          className="dl-action"
                          data-testid={`approve-deadline-${d.id}`}
                          onClick={() => {
                            hapticImpact('light');
                            setModerationTarget(d);
                            setModerationAction('approve');
                          }}
                        >
                          {strings.groups.moderationApprove}
                        </button>
                        <button
                          type="button"
                          className="dl-action dl-danger"
                          data-testid={`reject-deadline-${d.id}`}
                          onClick={() => {
                            hapticNotification('warning');
                            setModerationTarget(d);
                            setModerationAction('reject');
                          }}
                        >
                          {strings.groups.moderationReject}
                        </button>
                      </span>
                    }
                  >
                    {d.title}
                  </Cell>
                ))}
              </Section>
            ) : null}

            <Section header={strings.groups.dangerHeader} data-testid="danger-section">
              <Cell
                Component="button"
                type="button"
                className="dl-cell-button dl-danger"
                data-testid="open-delete-group"
                onClick={() => {
                  hapticNotification('warning');
                  setConfirmDelete(true);
                }}
              >
                {strings.groups.deleteGroup}
              </Cell>
            </Section>
          </>
        ) : inGroup ? (
          // Состав виден и участнику (ListMembers пускает любого члена группы):
          // показываем загрузку и ошибку, иначе секция на миг выглядела бы
          // пустой, а сбой запроса — как «в группе никого нет».
          <Section header={strings.groups.membersHeader} data-testid="members-section">
            {members.isLoading ? (
              <div className="dl-centered">
                <Spinner size="s" />
              </div>
            ) : members.isError ? (
              <div className="dl-error" role="alert" data-testid="members-error">
                {members.error instanceof Error ? members.error.message : strings.common.loadError}
              </div>
            ) : (
              memberList.map((member) => (
                <MemberCell
                  key={member.user_id}
                  member={member}
                  isMe={member.user_id === me}
                  canManage={false}
                  menuOpen={false}
                  onMenuToggle={() => {}}
                  onPromote={() => {}}
                  onDemote={() => {}}
                  onKick={() => {}}
                  tz={tz}
                />
              ))
            )}
          </Section>
        ) : null}

        {inGroup ? (
          <Section data-testid="leave-section">
            <Cell
              Component="button"
              type="button"
              className="dl-cell-button dl-danger"
              data-testid="open-leave"
              onClick={() => setConfirmLeave(true)}
            >
              {strings.groups.leave}
            </Cell>
          </Section>
        ) : null}
      </List>

      <InviteSheet
        open={inviteOpen}
        onOpenChange={setInviteOpen}
        groupID={groupID}
        tz={tz}
        hasBinding={binding !== null}
        onCreated={() => {
          void invites.refetch();
        }}
      />

      <ConfirmDialog
        open={confirmLeave}
        title={strings.groups.leaveConfirm}
        description={
          binding ? strings.groups.leaveConfirmHint : strings.groups.leaveConfirmUnboundHint
        }
        confirmLabel={strings.groups.leave}
        confirmTestId="confirm-leave"
        onConfirm={() => {
          setConfirmLeave(false);
          void run(async () => {
            await leave.mutateAsync(groupID);
            navigate('/groups');
          }, mutationErrorMessage);
        }}
        onCancel={() => setConfirmLeave(false)}
      />

      <ConfirmDialog
        open={confirmDelete}
        title={tpl(strings.groups.deleteConfirm, group.slug)}
        description={strings.groups.deleteConfirmHint}
        confirmLabel={strings.groups.deleteGroup}
        confirmTestId="confirm-delete-group"
        onConfirm={() => {
          setConfirmDelete(false);
          void run(async () => {
            await removeGroup.mutateAsync(groupID);
            navigate('/groups');
          }, mutationErrorMessage);
        }}
        onCancel={() => setConfirmDelete(false)}
      />

      <ConfirmDialog
        open={confirmRevoke}
        title={strings.groups.inviteRevokeConfirm}
        description={strings.groups.inviteRevokeConfirmHint}
        confirmLabel={strings.groups.inviteRevoke}
        confirmTestId="confirm-invite-revoke"
        onConfirm={() => {
          const target = revokeTarget;
          setConfirmRevoke(false);
          setRevokeTarget(null);
          if (!target) return;
          void run(async () => {
            await revokeInvite.mutateAsync({ groupID, code: target.code });
            void invites.refetch();
          }, mutationErrorMessage);
        }}
        onCancel={() => {
          setConfirmRevoke(false);
          setRevokeTarget(null);
        }}
      />

      <ConfirmDialog
        open={pendingKick !== null}
        title={strings.groups.kickConfirm}
        description={
          pendingKick ? tpl(strings.groups.kickConfirmHint, memberName(pendingKick)) : undefined
        }
        confirmLabel={strings.groups.kick}
        confirmTestId="confirm-kick"
        onConfirm={() => {
          const target = pendingKick;
          setPendingKick(null);
          if (!target) return;
          void run(() => kick.mutateAsync({ groupID, userID: target.user_id }));
        }}
        onCancel={() => setPendingKick(null)}
      />

      <ConfirmDialog
        open={moderationTarget !== null}
        title={
          moderationTarget
            ? tpl(
                moderationAction === 'approve'
                  ? strings.groups.moderationApproveConfirm
                  : strings.groups.moderationRejectConfirm,
                moderationTarget.title,
              )
            : strings.groups.moderationApproveConfirm.replace('%s', '')
        }
        description={
          moderationAction === 'approve'
            ? strings.groups.moderationApproveHint
            : strings.groups.moderationRejectHint
        }
        confirmLabel={
          moderationAction === 'approve'
            ? strings.groups.moderationApprove
            : strings.groups.moderationReject
        }
        confirmTestId={`confirm-${moderationAction}-deadline`}
        onConfirm={() => {
          const target = moderationTarget;
          setModerationTarget(null);
          if (!target) return;
          void run(async () => {
            if (moderationAction === 'approve') {
              await approveDeadline.mutateAsync(target.id);
              setNotice(tpl(strings.groups.moderationApprovedNotice, target.title));
            } else {
              await rejectDeadline.mutateAsync(target.id);
            }
            void pending.refetch();
          }, mutationErrorMessage);
        }}
        onCancel={() => setModerationTarget(null)}
      />
    </Screen>
  );
}
