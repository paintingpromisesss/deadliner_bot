// Бейдж роли в группе (спека §9, экран 5): «админ» подсвечен, «участник» —
// нейтральный, пустая роль (не участник, в подсказках поиска) — серая подпись
// «не участник». Отдельный компонент, потому что роль рисуется в трёх местах:
// список групп, поиск и админ-панель.
import { Badge } from './ui';
import { strings } from '../lib/strings';
import type { GroupRole } from '../lib/groups';

interface RoleBadgeProps {
  role: GroupRole;
}

export function RoleBadge({ role }: RoleBadgeProps) {
  if (role === 'admin') {
    return (
      <Badge mode="primary" data-testid="role-badge" data-role="admin">
        {strings.groups.roleAdmin}
      </Badge>
    );
  }
  if (role === 'member') {
    return (
      <Badge mode="secondary" data-testid="role-badge" data-role="member">
        {strings.groups.roleMember}
      </Badge>
    );
  }
  return (
    <Badge mode="gray" data-testid="role-badge" data-role="none">
      {strings.groups.roleNone}
    </Badge>
  );
}

/** Подпись роли строкой (для subtitle ячеек, где бейдж избыточен). */
export function roleLabel(role: GroupRole): string {
  if (role === 'admin') return strings.groups.roleAdmin;
  if (role === 'member') return strings.groups.roleMember;
  return strings.groups.roleNone;
}
