// Карточка дедлайна в стиле Wallet (спека §9): hero-карточка ближайшего
// дедлайна с крупным сроком и живым обратным отсчётом и компактная cell-строка
// для списков.
//
// Отсчёт обновляется раз в минуту локальным интервалом: точность «до минуты»
// соответствует гранулярности текста («осталось 2 дня 3 часа»), а интервал
// дешевле любого внешнего таймера и не зависит от сети.
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { countdownTo, formatDueOrDash, isValidInstant } from '../lib/format';
import { isGroupDeadline } from '../lib/deadlineGroups';
import type { Deadline } from '../lib/deadlines';
import { strings, tpl } from '../lib/strings';

/**
 * Подпись обратного отсчёта. Неразобранный срок (битый DTO) — это «срок истёк»,
 * а не «просрочен на NaN дней»: враньё в интерфейсе хуже отсутствия числа.
 * Формулировки — из каталога строк, здесь только выбор ключа.
 */
function countdownText(due: Date | string | number, now: Date): string {
  if (!isValidInstant(due)) return strings.deadlines.countdownDue;
  const countdown = countdownTo(due, now);
  if (countdown.direction === 'now') return strings.deadlines.countdownDue;
  return countdown.direction === 'left'
    ? tpl(strings.deadlines.countdownLeft, countdown.duration)
    : tpl(strings.deadlines.countdownOverdue, countdown.duration);
}

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
  const overdue = isValidInstant(deadline.due_at) && countdownTo(deadline.due_at, now).direction === 'overdue';

  return (
    <div className="dl-hero">
      <div className="dl-hero__card">
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
          {formatDueOrDash(deadline.due_at, tz)}
        </div>
        <div
          className={overdue ? 'dl-countdown dl-countdown--overdue' : 'dl-countdown'}
          data-testid="hero-countdown"
        >
          {countdownText(deadline.due_at, now)}
        </div>
      </div>
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
  const overdue =
    isValidInstant(deadline.due_at) && countdownTo(deadline.due_at, now).direction === 'overdue';
  const subtitle = `${scopeLabel(deadline, groupSlug)} · ${formatDueOrDash(deadline.due_at, tz)}`;

  return (
    <div
      className="dl-cell-wrap"
      data-overdue={overdue ? 'true' : 'false'}
      data-testid={`cell-${deadline.id}`}
    >
      <button
        type="button"
        className={overdue ? 'dl-cell dl-cell--overdue' : 'dl-cell'}
        onClick={() => onSelect(deadline)}
      >
        <span className="dl-cell__main">
          <span className="dl-cell__title">{deadline.title}</span>
          <span className="dl-cell__subtitle">{subtitle}</span>
        </span>
      </button>
      {actions ? <div className="dl-cell-actions">{actions}</div> : null}
    </div>
  );
}