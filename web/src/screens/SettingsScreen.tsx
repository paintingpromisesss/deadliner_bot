// Экран настроек (спека §9, экран 6): профиль, часовой пояс, общий дефолт
// ЛС-дублей, переопределения по группам, выход. Две секции уведомлений — две
// сущности backend: users.dm_notify_default (PATCH /me) и membership.dm_notify
// (PATCH /notifications/settings). В подписи группы показывается ЭФФЕКТИВНОЕ
// значение (override ? своё : дефолт), а не сырое поле membership — иначе
// выключенный переключатель мог бы означать фактически включённые дубли.
import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  Caption,
  Cell,
  List,
  Section,
  Select,
  Spinner,
  Switch,
} from '../components/ui';
import { Screen } from '../components/Screen';
import { useAuthStore } from '../stores/auth';
import { NOTIFICATIONS_QUERY_KEY, useNotificationSettings, usePatchNotificationSettings } from '../lib/queries';
import { strings, tpl } from '../lib/strings';
import { hapticNotification } from '../lib/tma';
import type { NotificationGroup } from '../lib/groups';

/** Часовые пояса, предлагаемые в селекте (спека: tz пользователя, MSK дефолт). */
const TIMEZONES = [
  'Europe/Kaliningrad',
  'Europe/Moscow',
  'Europe/Samara',
  'Asia/Yekaterinburg',
  'Asia/Omsk',
  'Asia/Krasnoyarsk',
  'Asia/Irkutsk',
  'Asia/Yakutsk',
  'Asia/Vladivostok',
  'Asia/Magadan',
  'Asia/Kamchatka',
  'UTC',
];

/** Текущий tz браузера, если он есть в списке и не совпадает с профилем. */
function browserTZ(): string | null {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return tz && TIMEZONES.includes(tz) ? tz : null;
  } catch {
    return null;
  }
}

export function SettingsScreen() {
  const user = useAuthStore((s) => s.user);
  const patchMe = useAuthStore((s) => s.patchMe);
  const queryClient = useQueryClient();

  const settings = useNotificationSettings();
  const patchSettings = usePatchNotificationSettings();

  const [tz, setTZ] = useState(user?.tz ?? '');
  const [dm, setDm] = useState(user?.dm_notify_default ?? true);
  const [groupError, setGroupError] = useState<string | null>(null);

  useEffect(() => {
    if (user) {
      setTZ(user.tz);
      setDm(user.dm_notify_default);
    }
  }, [user]);

  if (!user) {
    return (
      <Screen title={strings.settings.loading}>
        <Spinner size="m" />
      </Screen>
    );
  }

  const suggested = browserTZ();

  async function updateProfile(newTZ: string, newDm: boolean) {
    setTZ(newTZ);
    setDm(newDm);
    try {
      await patchMe({ tz: newTZ, dm_notify_default: newDm });
      await queryClient.invalidateQueries({ queryKey: NOTIFICATIONS_QUERY_KEY });
      hapticNotification('success');
    } catch {
      hapticNotification('error');
    }
  }

  /** Переключение дублей группы: PATCH с group_id и текущим значением. */
  function toggleGroup(item: NotificationGroup, next: boolean) {
    setGroupError(null);
    void patchSettings
      .mutateAsync({ group_id: item.group_id, dm_notify: next })
      .catch((e: unknown) => {
        setGroupError(e instanceof Error ? e.message : strings.common.actionFailed);
      });
  }

  /** «Сбросить к общему»: dm_notify: null снимает переопределение (backend §5.2). */
  function inheritGroup(item: NotificationGroup) {
    setGroupError(null);
    void patchSettings.mutateAsync({ group_id: item.group_id, dm_notify: null }).catch((e: unknown) => {
      setGroupError(e instanceof Error ? e.message : strings.common.actionFailed);
    });
  }

  const groupSettings = settings.data?.groups ?? [];

  return (
    <Screen title={strings.settings.title}>
      <List>
        <Section header={strings.settings.profileHeader}>
          <Cell
            before={<span className="dl-avatar">{(user.first_name || '?').slice(0, 1).toUpperCase()}</span>}
            subtitle={user.username ? `@${user.username}` : strings.settings.noUsername}
            after={user.is_superadmin ? <Caption level="1">superadmin</Caption> : undefined}
            multiline
          >
            {user.first_name || strings.settings.defaultName}
          </Cell>
        </Section>

        <Section header={strings.settings.tzHeader} footer={strings.settings.tzFooter}>
          <Cell multiline>
            <Select
              value={tz}
              onChange={(e) => void updateProfile(e.target.value, dm)}
              aria-label={strings.settings.tzHeader}
            >
              {!TIMEZONES.includes(tz) && tz ? <option value={tz}>{tz}</option> : null}
              {TIMEZONES.map((zone) => (
                <option key={zone} value={zone}>
                  {zone}
                </option>
              ))}
            </Select>
          </Cell>
          {suggested && suggested !== tz ? (
            <Cell
              Component="button"
              type="button"
              className="dl-cell-button"
              subtitle={tpl(strings.settings.tzDeviceHint, suggested)}
              onClick={() => void updateProfile(suggested, dm)}
            >
              {strings.settings.tzUseDevice}
            </Cell>
          ) : null}
        </Section>

        <Section header={strings.settings.notificationsHeader}>
          <Cell
            Component="label"
            multiline
            subtitle={strings.settings.dmDefaultHint}
            after={<Switch checked={dm} onChange={(e) => void updateProfile(tz, e.target.checked)} />}
          >
            {strings.settings.dmDefault}
          </Cell>
        </Section>

        <Section
          header={strings.settings.groupsHeader}
          footer={strings.settings.groupsFooter}
          data-testid="group-notifications"
        >
          {settings.isLoading ? (
            <div className="dl-centered">
              <Spinner size="s" />
            </div>
          ) : settings.isError ? (
            <>
              <div className="dl-error" role="alert">
                {settings.error instanceof Error ? settings.error.message : strings.common.loadError}
              </div>
              <Cell
                Component="button"
                type="button"
                className="dl-cell-button"
                data-testid="group-notifications-retry"
                onClick={() => void settings.refetch()}
              >
                {strings.common.retry}
              </Cell>
            </>
          ) : groupSettings.length === 0 ? (
            <div className="dl-hint" data-testid="group-notifications-empty">
              {strings.settings.groupsEmpty}
            </div>
          ) : (
            groupSettings.map((item) => (
              <div key={item.group_id} data-testid={`group-notify-${item.group_id}`}>
                <Cell
                  Component="div"
                  multiline
                  subtitle={
                    item.override
                      ? `${item.slug} · ${item.dm_notify ? strings.settings.groupOverrideEnabled : strings.settings.groupOverrideDisabled}`
                      : item.slug
                  }
                  after={
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      {item.override ? (
                        <button
                          type="button"
                          className="dl-reset-btn"
                          data-testid={`group-inherit-${item.group_id}`}
                          disabled={patchSettings.isPending}
                          onClick={(e) => {
                            e.preventDefault();
                            e.stopPropagation();
                            inheritGroup(item);
                          }}
                          title={strings.settings.groupInherit}
                        >
                          {strings.settings.groupInherit}
                        </button>
                      ) : null}
                      <Switch
                        checked={item.dm_notify}
                        disabled={patchSettings.isPending}
                        data-testid={`group-notify-switch-${item.group_id}`}
                        onChange={(e) => toggleGroup(item, e.target.checked)}
                      />
                    </div>
                  }
                >
                  {item.title || item.slug}
                </Cell>
              </div>
            ))
          )}
          {groupError ? (
            <div className="dl-error" role="alert" data-testid="group-notify-error">
              {groupError}
            </div>
          ) : null}
        </Section>
      </List>
    </Screen>
  );
}
