// Карточка дедлайна в стиле Wallet (спека §9): hero-карточка ближайшего
// дедлайна с крупным сроком и живым обратным отсчётом и компактная cell-строка
// для списков.
//
// Отсчёт обновляется раз в минуту локальным интервалом: точность «до минуты»
// соответствует гранулярности текста («осталось 2 дня 3 часа»), а интервал
// дешевле любого внешнего таймера и не зависит от сети.
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Card, Cell } from '@telegram-apps/telegram-ui';
import { countdownTo, formatDue } from '../lib/format';
import { isGroupDeadline } from '../lib/deadlineGroups';
import type { Deadline } from '../lib/deadlines';
import { strings, tpl } from '../lib/strings';

/** Мгновение «сейчас» с обновлением раз в минуту (для обратного отсчёта). */
export function useMinuteTick(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 60_000);
    return () => window.clearInterval(id);
  }, []);
  return now;
}

/** Название типа дедлайна: «Личный» или слаг группы (передаётся снаружи). */
export function scopeLabel(deadline: Deadline, groupSlug?: string): string {
  if (!isGroupDeadline(deadline)) return strings.deadlines.personal;
  return groupSlug ?? `#${deadline.group_id}`;
}

interface DeadlineHeroProps {
  deadline: Deadline;
  tz: string;
  /** Слаг группы для подписи (если групповой). */
  groupSlug?: string;
  now: Date;
  onOpen?: () => void;
}

/**
 * Hero-карточка «Ближайший дедлайн»: крупная дата + обратный отсчёт (как баланс
 * в Wallet). Просроченный дедлайн — destructive-цвет.
 */
export function DeadlineHero({ deadline, tz, groupSlug, now, onOpen }: DeadlineHeroProps) {
  const countdown = countdownTo(deadline.due_at, now);
  const overdue = countdown.direction === 'overdue';

  return (
    <div className="dl-hero">
      <Card className="dl-hero__card">
        <div className="dl-hero__label">
          {overdue ? strings.deadlines.heroLabelOverdue : strings.deadlines.heroLabel}
        </div>
        <div className="dl-hero__title" data-testid="hero-title">
          {deadline.title}
        </div>
        <div className="dl-hero__scope">{scopeLabel(deadline, groupSlug)}</div>
        <div
          className={overdue ? 'dl-hero__due dl-hero__due--overdue' : 'dl-hero__due'}
          data-testid="hero-due"
        >
          {formatDue(deadline.due_at, tz)}
        </div>
        <div
          className={overdue ? 'dl-countdown dl-countdown--overdue' : 'dl-countdown'}
          data-testid="hero-countdown"
        >
          {countdown.direction === 'now'
            ? strings.deadlines.countdownDue
            : countdown.direction === 'left'
              ? tpl(strings.deadlines.countdownLeft, countdown.duration)
              : tpl(strings.deadlines.countdownOverdue, countdown.duration)}
        </div>
      </Card>
      {onOpen ? (
        <button type="button" className="dl-hero__overlay" aria-label={deadline.title} onClick={onOpen} />
      ) : null}
    </div>
  );
}

interface DeadlineCellProps {
  deadline: Deadline;
  tz: string;
  groupSlug?: string;
  now: Date;
  onSelect: (deadline: Deadline) => void;
  /** Дополнительные действия ячейки (выполнить/удалить) — кнопками справа. */
  actions?: ReactNode;
}

/**
 * Строка списка: заголовок, подпись «тип · срок», отметка просрочки. Тап —
 * открытие формы редактирования (спека §9: «тап cell → edit sheet»).
 */
export function DeadlineCell({
  deadline,
  tz,
  groupSlug,
  now,
  onSelect,
  actions,
}: DeadlineCellProps) {
  const overdue = countdownTo(deadline.due_at, now).direction === 'overdue';
  const subtitle = `${scopeLabel(deadline, groupSlug)} · ${formatDue(deadline.due_at, tz)}`;

  return (
    <div
      className="dl-cell-wrap"
      data-overdue={overdue ? 'true' : 'false'}
      data-testid={`cell-${deadline.id}`}
    >
      <Cell
        Component="button"
        type="button"
        className={overdue ? 'dl-cell-button dl-cell--overdue' : 'dl-cell-button'}
        subtitle={subtitle}
        multiline
        onClick={() => onSelect(deadline)}
      >
        {deadline.title}
      </Cell>
      {actions ? <div className="dl-cell-actions">{actions}</div> : null}
    </div>
  );
}