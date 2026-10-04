// Подсказки слага (спека §9, экран 5): список найденных групп с их ролью.
// Публичного входа в API нет — только по инвайт-коду (спека §3.3), поэтому
// подсказка показывает состояние группы («вы уже в группе» / нужен инвайт),
// а не кнопку, гарантированно упирающуюся в 403/404. Мои группы исключены:
// их список уже на этом же экране.
import { List, Section, Spinner } from './ui';
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
  /**
   * Совпадения были, но все — уже мои группы. Отличается от «ничего не
   * найдено»: пользователь искал свою же группу, и подсказка обязана сказать,
   * где она, а не что её нет.
   */
  allMine: boolean;
}

/** Подсказки поиска: только непустой результат либо явная подсказка. */
export function SlugPicker({ items, loading, searched, allMine }: SlugPickerProps) {
  if (loading) {
    return (
      <div className="dl-search-state" data-testid="search-loading">
        <Spinner size="s" />
      </div>
    );
  }

  if (items.length === 0) {
    if (!searched) return null;
    if (allMine) {
      return (
        <div className="dl-hint" data-testid="search-all-mine">
          <div>{strings.groups.searchAllMine}</div>
          <div className="dl-hint__sub">{strings.groups.searchAllMineHint}</div>
        </div>
      );
    }
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
          <SlugSuggestion key={item.group.id} item={item} />
        ))}
      </Section>
    </List>
  );
}

interface SlugSuggestionProps {
  item: GroupSummary;
}

/** Кнопка-подсказка: слаг + пояснение «вход по инвайту» + роль-бейдж. */
function SlugSuggestion({ item }: SlugSuggestionProps) {
  return (
    <button
      type="button"
      className="dl-cell dl-cell--multiline"
      data-testid={`suggestion-${item.group.id}`}
      onClick={() => {
        hapticImpact('light');
        navigate(`/groups/${item.group.id}`);
      }}
    >
      <span className="dl-cell__before">
        <SlugAvatar slug={item.group.slug} />
      </span>
      <span className="dl-cell__main">
        <span className="dl-cell__title">{item.group.slug}</span>
        <span className="dl-cell__subtitle">{strings.groups.searchJoinHint}</span>
      </span>
      <span className="dl-cell__after">
        <RoleBadge role={item.role} />
      </span>
    </button>
  );
}

/** Подсказки без групп пользователя (их показывает основной список). */
export function filterSuggestions(items: GroupSummary[] | undefined): GroupSummary[] {
  return (items ?? []).filter((item) => item.role === '');
}
