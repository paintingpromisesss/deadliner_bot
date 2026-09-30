// Экран «Дедлайны» (главный; спека §9, экран 2, стиль Wallet): hero-карточка
// ближайшего дедлайна с обратным отсчётом, чип-фильтры «Все/Личные/Групповые»,
// сегменты «Просрочено/Сегодня/7 дней/Позже» cell-списками и FAB «+».
//
// Действия над дедлайном доступны двумя путями: тап по строке открывает форму
// редактирования (там «выполнить»/«удалить»), кнопки справа на строке делают то
// же самое без открытия формы. Удаление подтверждается в обоих местах.
import { useMemo, useState } from 'react';
import { Button, Cell, List, Placeholder, Section, Spinner } from '@telegram-apps/telegram-ui';
import { Screen } from '../../components/Screen';
import { DeadlineCell, DeadlineHero, useMinuteTick } from '../../components/DeadlineCard';
import { DeadlineSheet } from '../../components/DeadlineSheet';
import { FilterChips } from '../../components/FilterChips';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { navigate } from '../../router';
import { useAuthStore } from '../../stores/auth';
import {
  SEGMENTS,
  SCOPE_FILTERS,
  filterByScope,
  nearestDeadline,
  onlyActive,
  segmentize,
  type ScopeFilter,
  type Segment,
} from '../../lib/deadlineGroups';
import { asDeadlines, groupSlugMap, useAllDeadlines, useCompleteDeadline, useDeleteDeadline, useMyGroups } from '../../lib/queries';
import type { Deadline } from '../../lib/deadlines';
import { hapticImpact } from '../../lib/tma';
import { strings, tpl } from '../../lib/strings';

const SCOPE_LABELS: Record<ScopeFilter, string> = {
  all: strings.deadlines.allShort,
  personal: strings.deadlines.personalShort,
  group: strings.deadlines.groupShort,
};

const SEGMENT_LABELS: Record<Segment, string> = {
  overdue: strings.deadlines.segmentOverdue,
  today: strings.deadlines.segmentToday,
  week: strings.deadlines.segmentWeek,
  later: strings.deadlines.segmentLater,
};

export function DeadlinesScreen() {
  const tz = useAuthStore((s) => s.user?.tz ?? 'Europe/Moscow');
  const [scope, setScope] = useState<ScopeFilter>('all');
  const [sheetOpen, setSheetOpen] = useState(false);
  const [editing, setEditing] = useState<Deadline | null>(null);
  // Удаление из списка подтверждается: это необратимое действие (soft delete),
  // случайный тап по строке в списке не должен стирать дедлайн.
  const [pendingDelete, setPendingDelete] = useState<Deadline | null>(null);

  const now = useMinuteTick();
  const query = useAllDeadlines();
  const groups = useMyGroups();
  const slugs = useMemo(() => groupSlugMap(groups.data), [groups.data]);
  const complete = useCompleteDeadline();
  const remove = useDeleteDeadline();

  const all = asDeadlines(query.data);
  // Фильтр/сегменты/hero считаются по активным: выполненные и архивные в
  // списке не показываются, но и «пусто» объявлять из-за них неверно.
  const active = useMemo(() => onlyActive(all), [all]);
  const filtered = useMemo(() => filterByScope(active, scope), [active, scope]);
  const segmented = useMemo(() => segmentize(filtered, tz, now), [filtered, tz, now]);
  const nearest = useMemo(() => nearestDeadline(filtered, now), [filtered, now]);

  function openCreate() {
    setEditing(null);
    setSheetOpen(true);
  }

  function openEdit(deadline: Deadline) {
    setEditing(deadline);
    setSheetOpen(true);
  }

  const empty = active.length === 0;
  const nothingInFilter = !empty && filtered.length === 0;

  return (
    <Screen title={strings.deadlines.title}>
      {query.isLoading ? (
        <div className="dl-centered">
          <Spinner size="m" />
        </div>
      ) : query.isError ? (
        <Placeholder
          header={strings.common.loadError}
          description={query.error instanceof Error ? query.error.message : undefined}
          action={
            <Button size="m" onClick={() => void query.refetch()}>
              {strings.common.retry}
            </Button>
          }
        />
      ) : (
        <>
          {nearest ? (
            <DeadlineHero
              deadline={nearest}
              tz={tz}
              groupSlug={slugs.get(nearest.group_id ?? -1)}
              now={now}
              onOpen={() => openEdit(nearest)}
            />
          ) : null}

          {empty ? (
            <Placeholder
              header={strings.deadlines.emptyAll}
              description={strings.deadlines.emptyAllHint}
              action={
                <Button size="m" onClick={openCreate}>
                  {strings.deadlines.addButton}
                </Button>
              }
            />
          ) : (
            <>
              <div className="dl-filters">
                <FilterChips
                  label={strings.deadlines.filtersLabel}
                  options={SCOPE_FILTERS.map((value) => ({ value, label: SCOPE_LABELS[value] }))}
                  value={scope}
                  onChange={setScope}
                />
              </div>

              {nothingInFilter ? (
                <Placeholder
                  header={strings.deadlines.emptyFiltered}
                  description={strings.deadlines.emptyFilteredHint}
                />
              ) : (
                <List>
                  {SEGMENTS.map((segment) => {
                    const items = segmented[segment];
                    if (items.length === 0) return null;
                    return (
                      <Section
                        key={segment}
                        header={SEGMENT_LABELS[segment]}
                        data-testid={`segment-${segment}`}
                      >
                        {items.map((deadline) => (
                          <DeadlineCell
                            key={deadline.id}
                            deadline={deadline}
                            tz={tz}
                            groupSlug={slugs.get(deadline.group_id ?? -1)}
                            now={now}
                            onSelect={openEdit}
                            actions={
                              <>
                                <button
                                  type="button"
                                  className="dl-action"
                                  title={strings.deadlines.complete}
                                  aria-label={strings.deadlines.complete}
                                  data-testid={`complete-${deadline.id}`}
                                  onClick={() => void complete.mutateAsync(deadline.id)}
                                >
                                  ✓
                                </button>
                                <button
                                  type="button"
                                  className="dl-action dl-danger"
                                  title={strings.sheet.actionDelete}
                                  aria-label={strings.sheet.actionDelete}
                                  data-testid={`delete-${deadline.id}`}
                                  onClick={() => setPendingDelete(deadline)}
                                >
                                  ✕
                                </button>
                              </>
                            }
                          />
                        ))}
                      </Section>
                    );
                  })}
                  <Cell
                    Component="button"
                    type="button"
                    className="dl-cell-button"
                    subtitle={tpl(strings.deadlines.totalCount, active.length)}
                    onClick={() => navigate('/calendar')}
                  >
                    {strings.calendar.title}
                  </Cell>
                </List>
              )}
            </>
          )}

          <button
            type="button"
            className="dl-fab"
            aria-label={strings.deadlines.addButton}
            data-testid="fab-add"
            onClick={() => {
              hapticImpact('medium');
              openCreate();
            }}
          >
            +
          </button>

          <DeadlineSheet
            open={sheetOpen}
            onOpenChange={setSheetOpen}
            deadline={editing}
            tz={tz}
            onSaved={() => setEditing(null)}
          />

          <ConfirmDialog
            open={pendingDelete !== null}
            title={strings.sheet.confirmDelete}
            description={strings.sheet.confirmDeleteHint}
            confirmLabel={strings.sheet.actionDelete}
            confirmTestId={`confirm-delete-${pendingDelete?.id ?? 'none'}`}
            onConfirm={() => {
              const target = pendingDelete;
              setPendingDelete(null);
              if (target) void remove.mutateAsync(target.id);
            }}
            onCancel={() => setPendingDelete(null)}
          />
        </>
      )}
    </Screen>
  );
}