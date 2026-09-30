// Подсказки слага (спека §9, экран 5: «поиск/подсказка слага»): поле ввода +
// выпадающий список найденных групп с их ролью.
//
// Публичного входа в группу в API нет — вступление только по инвайт-коду
// (спека §3.3). Поэтому подсказка честно показывает состояние группы:
// «вы уже в группе» (роль admin/member) либо пояснение, что нужен инвайт, —
// вместо кнопки, которая гарантированно упиралась бы в 403/404.
//
// Уже вступленные группы из подсказок исключаются: список моих групп есть на
// том же экране, и дублировать его в поиске незачем.
import { Cell, List, Section, Spinner } from '@telegram-apps/telegram-ui';
import { RoleBadge } from './RoleBadge';
import { SlugAvatar } from './SlugAvatar';
import { navigate } from '../router';
import { strings } from '../lib/strings';
import { hapticImpact } from '../lib/tma';
import type { GroupSummary } from '../lib/groups';

interface SlugPickerProps {
  /** Отсортированные подсказки (уже без групп пользователя). */
  items: GroupSummary[];
  loading: boolean;
  /** Запрос отправлен и вернул пусто (не «ещё не искали»). */
  searched: boolean;
}

/** Подсказки поиска: только непустой результат либо явное «ничего не найдено». */
export function SlugPicker({ items, loading, searched }: SlugPickerProps) {
  if (loading) {
    return (
      <div className="dl-search-state" data-testid="search-loading">
        <Spinner size="s" />
      </div>
    );
  }

  if (items.length === 0) {
    if (!searched) return null;
    return (
      <div className="dl-hint" data-testid="search-empty">
        <div>{strings.groups.searchEmpty}</div>
        <div className="dl-hint__sub">{strings.groups.searchEmptyHint}</div>
      </div>
    );
  }

  return (
    <List data-testid="slug-suggestions">
      <Section header={strings.groups.searchSuggestions}>
        {items.map((item) => (
          <Cell
            key={item.group.id}
            Component="button"
            type="button"
            className="dl-cell-button"
            before={<SlugAvatar slug={item.group.slug} />}
            subtitle={strings.groups.searchJoinHint}
            after={<RoleBadge role={item.role} />}
            multiline
            data-testid={`suggestion-${item.group.id}`}
            onClick={() => {
              hapticImpact('light');
              navigate(`/groups/${item.group.id}`);
            }}
          >
            {item.group.slug}
          </Cell>
        ))}
      </Section>
    </List>
  );
}

/** Подсказки без групп пользователя (их показывает основной список). */
export function filterSuggestions(items: GroupSummary[] | undefined): GroupSummary[] {
  return (items ?? []).filter((item) => item.role === '');
}