// Экран группы (спека §9, экраны 5 и 7): шапка со статусом, привязка чата,
// участники, роли и админ-панель (участники, инвайты, claim, danger zone).
//
// Гейт по роли — не украшение: участнику backend отдаст 403 на изменяющие
// вызовы (requireAdmin), поэтому админ-панель ему не рендерится вовсе, а
// единственная доступная ему операция — «Выйти» — вынесена отдельно. Кнопки,
// гарантированно приводящие к отказу, в интерфейсе не показываем.
//
// Список инвайтов ведём в состоянии сессии: API отдаёт plaintext-код только в
// момент создания (в БД хранится SHA-256), GET-списка кодов нет — поэтому
// показать можно ровно то, что создано на этом экране, и об этом сказано
// подписью блока.
import { useEffect, useState } from 'react';
import { Button, Cell, List, Placeholder, Section, Spinner } from '@telegram-apps/telegram-ui';
import { Screen } from '../../components/Screen';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { ClaimSheet, RevokeClaimDialog } from '../../components/ClaimSheet';
import { InviteSheet, inviteSessionSubtitle } from '../../components/InviteSheet';
import { MemberCell, memberName } from '../../components/MemberCell';
import { RoleBadge, roleLabel } from '../../components/RoleBadge';
import { SlugAvatar } from '../../components/SlugAvatar';
import { navigate } from '../../router';
import { useAuthStore } from '../../stores/auth';
import {
  useDeleteGroup,
  useGroupDetail,
  useGroupMembers,
  useKickMember,
  useLeaveGroup,
  useRevokeClaim,
  useRevokeInvite,
  useSetMemberRole,
} from '../../lib/queries';
import { memberActionErrorMessage, mutationErrorMessage } from '../../lib/errorText';
import { formatDue } from '../../lib/format';
import { strings, tpl } from '../../lib/strings';
import { hapticImpact, hapticNotification } from '../../lib/tma';
import type { InviteCreated, Member } from '../../lib/groups';

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

  const leave = useLeaveGroup();
  const removeGroup = useDeleteGroup();
  const setRole = useSetMemberRole();
  const kick = useKickMember();
  const revokeInvite = useRevokeInvite();
  const revokeClaim = useRevokeClaim();

  const [actionError, setActionError] = useState<string | null>(null);
  const [menuFor, setMenuFor] = useState<number | null>(null);
  const [pendingKick, setPendingKick] = useState<Member | null>(null);
  const [confirmLeave, setConfirmLeave] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmRevoke, setConfirmRevoke] = useState(false);
  // Какой именно код отзываем: подтверждение одно, а кодов в сессии может быть
  // несколько — «последний созданный» стёр бы не тот, на который нажали.
  const [revokeTarget, setRevokeTarget] = useState<InviteCreated | null>(null);
  const [confirmClaimRevoke, setConfirmClaimRevoke] = useState(false);
  const [claimOpen, setClaimOpen] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  // Подтверждение claim: шит закрывается сразу после успеха, поэтому роль
  // «admin» надо отметить в интерфейсе — иначе единственным следствием
  // действия остаётся перерисованное меню роли.
  const [notice, setNotice] = useState<string | null>(null);
  // Сессионные инвайты: код живёт только здесь (сервер его не отдаёт повторно).
  const [sessionInvites, setSessionInvites] = useState<
    { invite: InviteCreated; role: 'admin' | 'member' }[]
  >([]);

  // Смена группы (переход из списка в другую) — чистое состояние: иначе чужие
  // сессионные коды и открытые меню переехали бы на новый экран.
  useEffect(() => {
    setActionError(null);
    setMenuFor(null);
    setSessionInvites([]);
    setNotice(null);
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
                  ? tpl(strings.groups.bindingTopic, binding.chat_title, binding.message_thread_id)
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

        {/* Claim: участнику и админу-без-роли в pending-группе; админ видит
            блок отзыва, если чужой код уже мог быть опубликован. */}
        {!isAdmin && inGroup ? (
          <Section header={strings.groups.claimStart} data-testid="claim-section">
            {binding ? (
              <Cell
                Component="button"
                type="button"
                className="dl-cell-button"
                data-testid="open-claim"
                subtitle={strings.groups.claimHint}
                multiline
                onClick={() => {
                  hapticImpact('light');
                  setClaimOpen(true);
                }}
              >
                {strings.groups.claimStart}
              </Cell>
            ) : (
              <Cell multiline data-testid="claim-no-binding">
                {tpl(strings.groups.claimNoBindingHint, group.slug)}
              </Cell>
            )}
          </Section>
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
                multiline
                subtitle={strings.groups.invitesHint}
                onClick={() => {
                  hapticImpact('light');
                  setInviteOpen(true);
                }}
              >
                {strings.groups.inviteCreate}
              </Cell>
              {/* В БД хранится только хэш кода, поэтому сервер не может
                  показать уже выданные коды: список ведём в состоянии сессии
                  и честно об этом пишем. */}
              <Cell multiline data-testid="invites-session-note">
                {strings.groups.inviteSessionOnly}
              </Cell>
              {sessionInvites.map(({ invite, role: inviteRole }) => (
                  <Cell
                    key={invite.code}
                    multiline
                    data-testid={`session-invite-${invite.code}`}
                    subtitle={inviteSessionSubtitle(invite, inviteRole, tz)}
                    after={
                      <button
                        type="button"
                        className="dl-action dl-danger"
                        aria-label={strings.groups.inviteRevoke}
                        data-testid={`revoke-invite-${invite.code}`}
                        onClick={() => {
                          setRevokeTarget(invite);
                          setConfirmRevoke(true);
                        }}
                      >
                        ✕
                      </button>
                    }
                  >
                    <span className="dl-code">{invite.code}</span>
                  </Cell>
                ))}
            </Section>

            <Section header={strings.groups.claimRevoke} data-testid="claim-admin-section">
              <Cell
                Component="button"
                type="button"
                className="dl-cell-button"
                data-testid="open-claim-revoke"
                multiline
                subtitle={strings.groups.claimAdminHint}
                onClick={() => {
                  // Отзыв кода — потенциально конфликтное действие (кто-то
                  // мог запросить смену старосты): предупреждающий отклик.
                  hapticNotification('warning');
                  setConfirmClaimRevoke(true);
                }}
              >
                {strings.groups.claimRevoke}
              </Cell>
            </Section>

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

      <ClaimSheet
        open={claimOpen}
        onOpenChange={setClaimOpen}
        groupID={groupID}
        slug={group.slug}
        hasBinding={binding !== null}
        onConfirmed={() => {
          setNotice(tpl(strings.groups.claimSuccess, group.slug));
          void detail.refetch();
        }}
      />

      <InviteSheet
        open={inviteOpen}
        onOpenChange={setInviteOpen}
        groupID={groupID}
        tz={tz}
        onCreated={(invite, inviteRole) =>
          setSessionInvites((prev) => [...prev, { invite, role: inviteRole }])
        }
      />

      <ConfirmDialog
        open={confirmLeave}
        title={strings.groups.leaveConfirm}
        description={strings.groups.leaveConfirmHint}
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
            setSessionInvites((prev) => prev.filter((x) => x.invite.code !== target.code));
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

      <RevokeClaimDialog
        open={confirmClaimRevoke}
        onConfirm={() => {
          setConfirmClaimRevoke(false);
          void run(() => revokeClaim.mutateAsync(groupID), mutationErrorMessage);
        }}
        onCancel={() => setConfirmClaimRevoke(false)}
      />
    </Screen>
  );
}