// Строка участника группы (спека §9, экран 7): имя, роль-бейдж, меню действий
// (promote/demote/kick).
//
// Меню не «swipe-action», а раскрывающийся блок кнопок под ячейкой: свайп
// недоступен с клавиатуры и неочевиден в Mini App, а действия здесь
// необратимые — промах по свайпу стоил бы участнику членства.
//
// Себе действия не показываем: backend отвергнет демоут/кик последнего админа
// (409 last_admin), а «выйти» — отдельная кнопка на экране группы. Так
// интерфейс не предлагает заведомо неудачное действие.
import { Cell } from '@telegram-apps/telegram-ui';
import { RoleBadge } from './RoleBadge';
import { strings, tpl } from '../lib/strings';
import { formatDue } from '../lib/format';
import type { Member } from '../lib/groups';

interface MemberCellProps {
  member: Member;
  /** Текущий пользователь: для него меню действий не рисуется. */
  isMe: boolean;
  /** true — роль текущего пользователя admin (иначе действий нет). */
  canManage: boolean;
  /** Меню открыто (управляется родителем: одновременно только одно). */
  menuOpen: boolean;
  onMenuToggle: () => void;
  onPromote: (member: Member) => void;
  onDemote: (member: Member) => void;
  onKick: (member: Member) => void;
  tz: string;
}

/** Отображаемое имя участника: имя, иначе @username, иначе id. */
export function memberName(member: Member): string {
  if (member.first_name.trim()) return member.first_name;
  if (member.username.trim()) return `@${member.username}`;
  return `#${member.user_id}`;
}

export function MemberCell({
  member,
  isMe,
  canManage,
  menuOpen,
  onMenuToggle,
  onPromote,
  onDemote,
  onKick,
  tz,
}: MemberCellProps) {
  const name = memberName(member);
  const subtitle = member.username ? `@${member.username}` : undefined;
  const showMenu = canManage && !isMe;

  return (
    <div className="dl-member" data-testid={`member-${member.user_id}`}>
      <Cell
        Component={showMenu ? 'button' : 'div'}
        // Cell с onClick обязан быть фокусируемым: иначе меню действий
        // недоступно с клавиатуры.
        {...(showMenu ? { type: 'button' as const } : {})}
        className={showMenu ? 'dl-cell-button' : undefined}
        subtitle={subtitle}
        after={<RoleBadge role={member.role} />}
        multiline
        onClick={showMenu ? onMenuToggle : undefined}
        aria-expanded={showMenu ? menuOpen : undefined}
      >
        {name}
      </Cell>

      <div className="dl-member__meta">
        {tpl(strings.groups.joinedAt, formatDue(member.joined_at, tz))}
      </div>

      {showMenu && menuOpen ? (
        <div className="dl-member__menu" data-testid={`member-menu-${member.user_id}`}>
          {member.role === 'member' ? (
            <button
              type="button"
              className="dl-menu-item"
              data-testid={`promote-${member.user_id}`}
              onClick={() => onPromote(member)}
            >
              {strings.groups.promote}
            </button>
          ) : (
            <button
              type="button"
              className="dl-menu-item"
              data-testid={`demote-${member.user_id}`}
              onClick={() => onDemote(member)}
            >
              {strings.groups.demote}
            </button>
          )}
          <button
            type="button"
            className="dl-menu-item dl-danger"
            data-testid={`kick-${member.user_id}`}
            onClick={() => onKick(member)}
          >
            {strings.groups.kick}
          </button>
        </div>
      ) : null}
    </div>
  );
}