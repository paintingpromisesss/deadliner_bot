// Экран «Группы» (спека §9, экран 5): список моих групп (аватар-слаг + роль),
// поиск-подсказка слага, создание и вступление по инвайт-коду. Роль — из
// того же ответа GET /groups: второй источник разошёлся бы с первым.
import { useMemo, useState } from 'react';
import { Button, Cell, Input, List, Placeholder, Section, Spinner } from '../../components/ui';
import { Screen } from '../../components/Screen';
import { CreateGroupSheet } from '../../components/CreateGroupSheet';
import { RedeemSheet } from '../../components/RedeemSheet';
import { RoleBadge, roleLabel } from '../../components/RoleBadge';
import { SlugAvatar } from '../../components/SlugAvatar';
import { SlugPicker, filterSuggestions } from '../../components/SlugPicker';
import { navigate } from '../../router';
import { useDebounced } from '../../lib/debounce';
import { useGroupSearch, useMyGroups } from '../../lib/queries';
import { strings, tpl } from '../../lib/strings';
import { hapticImpact } from '../../lib/tma';
import type { GroupSummary } from '../../lib/groups';

/** Статус группы в подписи строки: pending требует привязки чата. */
function statusLabel(status: string): string | undefined {
  switch (status) {
    case 'pending':
      return strings.groups.statusPending;
    case 'active':
      return undefined; // активная — норма, отдельная пометка не нужна
    case 'archived':
      return strings.groups.statusArchived;
    default:
      return undefined;
  }
}

export function GroupsScreen() {
  const groups = useMyGroups();
  const [query, setQuery] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [redeemOpen, setRedeemOpen] = useState(false);
  // Результат вступления: показываем строкой над списком, потому что сразу
  // переходить в группу — не то, чего ждёт пользователь, вводя код.
  const [notice, setNotice] = useState<string | null>(null);

  // Дебаунс до запроса: поиск по префиксу идёт по всей таблице групп, слать
  // его на каждый символ — это N запросов на один введённый номер.
  const debounced = useDebounced(query.trim());
  const search = useGroupSearch(debounced);
  const suggestions = useMemo(() => filterSuggestions(search.data), [search.data]);
  // Совпадения были, но после отсева моих групп ничего не осталось: это не
  // «ничего не найдено» — искомое лежит прямо выше, в списке групп.
  const allMine = (search.data?.length ?? 0) > 0 && suggestions.length === 0;

  function openGroup(id: number) {
    hapticImpact('light');
    navigate(`/groups/${id}`);
  }

  const mine: GroupSummary[] = groups.data ?? [];

  return (
    <Screen title={strings.groups.title}>
      <List>
        <Section>
          <Cell multiline>
            <Input
              header={strings.groups.searchLabel}
              placeholder={strings.groups.searchPlaceholder}
              value={query}
              data-testid="field-search"
              onChange={(e) => setQuery(e.target.value)}
            />
          </Cell>
        </Section>

        <SlugPicker
          items={suggestions}
          loading={search.isFetching && debounced.length > 0}
          searched={debounced.length > 0 && !search.isFetching}
          allMine={allMine}
        />

        {groups.isLoading ? (
          <div className="dl-centered">
            <Spinner size="m" />
          </div>
        ) : groups.isError ? (
          <Placeholder
            header={strings.common.loadError}
            description={groups.error instanceof Error ? groups.error.message : undefined}
            action={
              <Button size="m" onClick={() => void groups.refetch()}>
                {strings.common.retry}
              </Button>
            }
          />
        ) : mine.length === 0 ? (
          <Placeholder
            header={strings.groups.empty}
            description={strings.groups.emptyHint}
            action={
              <Button size="m" data-testid="empty-create" onClick={() => setCreateOpen(true)}>
                {strings.groups.createButton}
              </Button>
            }
          />
        ) : (
          <Section header={strings.groups.mineHeader} data-testid="my-groups">
            {notice ? (
              <div className="dl-hint" role="status" data-testid="groups-notice">
                {notice}
              </div>
            ) : null}
            {mine.map((item) => {
              const status = statusLabel(item.group.status);
              const subtitle = [roleLabel(item.role), status].filter(Boolean).join(' · ');
              return (
                <Cell
                  key={item.group.id}
                  Component="button"
                  type="button"
                  className="dl-cell-button"
                  before={<SlugAvatar slug={item.group.slug} />}
                  subtitle={subtitle}
                  after={<RoleBadge role={item.role} />}
                  multiline
                  data-testid={`group-${item.group.id}`}
                  onClick={() => openGroup(item.group.id)}
                >
                  {item.group.title || item.group.slug}
                </Cell>
              );
            })}
          </Section>
        )}

        <Section>
          <Cell
            Component="button"
            type="button"
            className="dl-cell-button"
            data-testid="open-create"
            onClick={() => {
              hapticImpact('light');
              setCreateOpen(true);
            }}
          >
            {strings.groups.createButton}
          </Cell>
          <Cell
            Component="button"
            type="button"
            className="dl-cell-button"
            data-testid="open-redeem"
            onClick={() => {
              hapticImpact('light');
              setRedeemOpen(true);
            }}
          >
            {strings.groups.redeemButton}
          </Cell>
        </Section>
      </List>

      <CreateGroupSheet
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(id) => openGroup(id)}
      />

      <RedeemSheet
        open={redeemOpen}
        onOpenChange={setRedeemOpen}
        onRedeemed={(_id, slug) => setNotice(tpl(strings.groups.redeemSuccess, slug))}
      />
    </Screen>
  );
}
