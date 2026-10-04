// Экран «Календарь» (спека §9, экран 3): месячная сетка Пн..Вс с точками на
// днях с дедлайнами, навигация по месяцам, тап по дню — список дня. Сетка —
// lib/calendar.ts, данные одним запросом GET /me/deadlines?from&to в границах
// месяца в tz пользователя (monthBounds).
import { useEffect, useMemo, useState } from 'react';
import { Button, Cell, List, Placeholder, Section, Spinner } from '../../components/ui';
import { Screen } from '../../components/Screen';
import { DeadlineCell, useMinuteTick } from '../../components/DeadlineCard';
import { DeadlineSheet } from '../../components/DeadlineSheet';
import { useAuthStore } from '../../stores/auth';
import { addMonths, buildMonth, dayKey, dayNumber, daysInMonth, monthBounds, type MonthDay } from '../../lib/calendar';
import {
  formatDayMonth,
  formatMonthTitle,
  fromWallClock,
  localToday,
  toDateInputValue,
  WEEKDAY_LABELS,
} from '../../lib/format';
import { groupByDay } from '../../lib/deadlineGroups';
import { asDeadlines, groupSlugMap, useAllDeadlines, useMyGroups } from '../../lib/queries';
import type { Deadline } from '../../lib/deadlines';
import { hapticImpact } from '../../lib/tma';
import { strings, tpl } from '../../lib/strings';

// Персистентность состояния между экранами (2.2).
let savedCursor: { year: number; month: number } | null = null;
let savedSelected: number | null = null;

export function resetCalendarState(): void {
  savedCursor = null;
  savedSelected = null;
}

export function CalendarScreen() {
  const tz = useAuthStore((s) => s.user?.tz ?? 'Europe/Moscow');
  const now = useMinuteTick();
  const today = useMemo(() => localToday(tz, now), [tz, now]);
  const todayNumber = dayNumber(today.year, today.month, today.day);

  // Курсор месяца и выбранный день — по умолчанию сегодня и сохраняются между табами (2.2).
  const [cursor, setCursor] = useState(() => savedCursor ?? { year: today.year, month: today.month });
  const [selected, setSelected] = useState<number | null>(() => savedSelected ?? todayNumber);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [editing, setEditing] = useState<Deadline | null>(null);

  function updateCursor(next: { year: number; month: number } | ((prev: { year: number; month: number }) => { year: number; month: number })) {
    setCursor((prev) => {
      const val = typeof next === 'function' ? next(prev) : next;
      savedCursor = val;
      return val;
    });
  }

  function updateSelected(val: number | null) {
    savedSelected = val;
    setSelected(val);
  }

  const bounds = useMemo(() => monthBounds(cursor.year, cursor.month, tz), [cursor, tz]);
  const query = useAllDeadlines({
    from: bounds.from.toISOString(),
    to: bounds.to.toISOString(),
  });
  const groups = useMyGroups();
  const slugs = useMemo(() => groupSlugMap(groups.data), [groups.data]);

  const grid = useMemo(() => buildMonth(cursor.year, cursor.month), [cursor]);
  const byDay = useMemo(() => groupByDay(asDeadlines(query.data), tz), [query.data, tz]);

  function shift(delta: number) {
    hapticImpact('light');
    const next = addMonths(cursor.year, cursor.month, delta);
    if (selected !== null) {
      const currentDay = new Date(selected * 86_400_000).getUTCDate();
      const maxDay = daysInMonth(next.year, next.month);
      const targetDay = Math.min(currentDay, maxDay);
      updateSelected(dayNumber(next.year, next.month, targetDay));
    }
    updateCursor(next);
  }

  function onPickDay(day: MonthDay) {
    hapticImpact('light');
    // Тап по добивке (день соседнего месяца) перелистывает месяц: иначе
    // выбранный день остался бы за пределами видимой сетки.
    if (!day.inMonth) updateCursor({ year: day.year, month: day.month });
    updateSelected(dayNumber(day.year, day.month, day.day));
  }

  const selectedDayDeadlines = selected === null ? [] : (byDay.get(selected) ?? []);
  // Мгновение выбранного дня — в tz пользователя (noon UTC тут не годится:
  // для UTC+12/+13 он приходится на следующее число, и подпись дня с
  // предзаполнением формы уезжали на сутки вперёд от подсвеченной ячейки).
  // Полдень настенного времени берём как «нейтральную» точку суток — так же,
  // как это делает форма при разборе date/time.
  const selectedDate = useMemo(() => {
    if (selected === null) return null;
    const day = grid.weeks.flat().find((d) => dayNumber(d.year, d.month, d.day) === selected);
    if (!day) return null;
    return fromWallClock(dayKey(day), '12:00', tz);
  }, [selected, grid, tz]);

  function openCreate() {
    setEditing(null);
    setSheetOpen(true);
  }

  useEffect(() => {
    const handler = () => openCreate();
    window.addEventListener('deadliner:open-create', handler);
    return () => window.removeEventListener('deadliner:open-create', handler);
  }, []);

  return (
    <Screen title={strings.calendar.title}>
      <div className="dl-cal">
        <div className="dl-cal__nav">
          <button
            type="button"
            className="dl-cal__nav-button"
            aria-label={strings.calendar.prevMonth}
            data-testid="cal-prev"
            onClick={() => shift(-1)}
          >
            ‹
          </button>
          <span className="dl-cal__nav-title" data-testid="cal-title">
            {formatMonthTitle(cursor.year, cursor.month, tz)}
          </span>
          <button
            type="button"
            className="dl-cal__nav-button"
            aria-label={strings.calendar.nextMonth}
            data-testid="cal-next"
            onClick={() => shift(1)}
          >
            ›
          </button>
        </div>

        <div className="dl-cal__grid">
          {WEEKDAY_LABELS.map((label) => (
            <div key={label} className="dl-cal__weekday">
              {label}
            </div>
          ))}
          {grid.weeks.flat().map((day) => {
            const number = dayNumber(day.year, day.month, day.day);
            const count = byDay.get(number)?.length ?? 0;
            const isToday = number === todayNumber;
            const isSelected = number === selected;
            const label = count > 0 ? `${dayKey(day)}, ${tpl(strings.calendar.markers, count)}` : dayKey(day);
            return (
              <button
                key={dayKey(day)}
                type="button"
                className="dl-cal__day"
                data-today={isToday ? 'true' : 'false'}
                data-selected={isSelected ? 'true' : 'false'}
                data-outside={day.inMonth ? 'false' : 'true'}
                data-count={count > 0 ? 'true' : 'false'}
                aria-label={label}
                aria-current={isSelected ? 'date' : undefined}
                data-testid={`day-${dayKey(day)}`}
                onClick={() => onPickDay(day)}
              >
                <span className="dl-cal__day-number">{day.day}</span>
                {count > 0 ? <span className="dl-cal__dot" aria-hidden /> : null}
              </button>
            );
          })}
        </div>
      </div>

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
      ) : selectedDate === null ? (
        <div className="dl-hint">{strings.calendar.dayEmptyHint}</div>
      ) : (
        <List>
          <Section header={formatDayMonth(selectedDate, tz)}>
            {selectedDayDeadlines.length === 0 ? (
              <Placeholder
                header={strings.calendar.dayEmptyHeader}
                description={strings.calendar.dayEmptyHint}
              />
            ) : (
              selectedDayDeadlines.map((deadline) => (
                <DeadlineCell
                  key={deadline.id}
                  deadline={deadline}
                  tz={tz}
                  groupSlug={slugs.get(deadline.group_id ?? -1)}
                  now={now}
                  onSelect={(d) => {
                    setEditing(d);
                    setSheetOpen(true);
                  }}
                />
              ))
            )}
          </Section>
        </List>
      )}

      {typeof document === 'undefined' || !document.querySelector('.dl-fab--global') ? (
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
      ) : null}

      <DeadlineSheet
        open={sheetOpen}
        onOpenChange={setSheetOpen}
        deadline={editing}
        tz={tz}
        initialDate={editing || !selectedDate ? undefined : toDateInputValue(selectedDate, tz)}
        onSaved={() => setEditing(null)}
      />
    </Screen>
  );
}
