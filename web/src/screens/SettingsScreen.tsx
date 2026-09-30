// Полноценный экран настроек (единственный «настоящий» экран Task 13):
// профиль, часовой пояс, флаг ЛС-уведомлений, выход.
import { useEffect, useState } from 'react';
import {
  Button,
  Caption,
  Cell,
  Input,
  List,
  Section,
  Select,
  Spinner,
  Switch,
} from '@telegram-apps/telegram-ui';
import { Screen } from '../components/Screen';
import { useAuthStore } from '../stores/auth';
import { hapticNotification } from '../lib/tma';

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
  const logout = useAuthStore((s) => s.logout);

  const [tz, setTZ] = useState(user?.tz ?? '');
  const [dm, setDm] = useState(user?.dm_notify_default ?? true);
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    if (user) {
      setTZ(user.tz);
      setDm(user.dm_notify_default);
    }
  }, [user]);

  if (!user) {
    return (
      <Screen title="Настройки">
        <Spinner size="m" />
      </Screen>
    );
  }

  const suggested = browserTZ();
  const serverTZ = user.tz;
  const dirty = tz !== serverTZ || dm !== user.dm_notify_default;

  async function save() {
    setSaving(true);
    setNote(null);
    try {
      await patchMe({ tz, dm_notify_default: dm });
      hapticNotification('success');
      setNote('Сохранено');
    } catch (e) {
      hapticNotification('error');
      setNote(e instanceof Error ? e.message : 'Не удалось сохранить');
    } finally {
      setSaving(false);
    }
  }

  return (
    <Screen title="Настройки">
      <List>
        <Section header="Профиль">
          <Cell
            before={<span className="dl-avatar">{(user.first_name || '?').slice(0, 1).toUpperCase()}</span>}
            subtitle={user.username ? `@${user.username}` : 'без username'}
            after={user.is_superadmin ? <Caption level="1">superadmin</Caption> : undefined}
            multiline
          >
            {user.first_name || 'Пользователь'}
          </Cell>
        </Section>

        <Section header="Часовой пояс" footer="В нём показываются даты дедлайнов и напоминаний.">
          <Cell multiline>
            <Select value={tz} onChange={(e) => setTZ(e.target.value)}>
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
              subtitle={`Часовой пояс устройства: ${suggested}`}
              onClick={() => setTZ(suggested)}
            >
              Использовать пояс устройства
            </Cell>
          ) : null}
        </Section>

        <Section header="Уведомления">
          <Cell
            Component="label"
            multiline
            subtitle="Копировать напоминания о групповых дедлайнах в личные сообщения. Для отдельных групп можно переопределить позже."
            after={<Switch checked={dm} onChange={(e) => setDm(e.target.checked)} />}
          >
            Дубли в личку по умолчанию
          </Cell>
        </Section>

        <Section>
          <div className="dl-row">
            <Button size="l" stretched loading={saving} disabled={!dirty} onClick={save}>
              Сохранить
            </Button>
          </div>
          <Cell multiline>
            <Input header="Telegram ID" value={String(user.telegram_id)} readOnly />
          </Cell>
        </Section>

        <Section footer={note ?? undefined}>
          <Cell
            Component="button"
            className="dl-danger"
            onClick={() => {
              hapticNotification('warning');
              void logout();
            }}
          >
            Выйти
          </Cell>
        </Section>
      </List>
    </Screen>
  );
}