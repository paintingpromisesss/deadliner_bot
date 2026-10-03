// Тест карточки дедлайна на битом due_at (страховка от «NaN.NaN.NaN NaN:NaN»
// и «просрочен на NaN дней»).
//
// Экранные пути фильтруют неразобранные сроки раньше (deadlineGroups.segmentize
// и nearestDeadline пропускают NaN), поэтому этот тест пинит защиту самого
// компонента — он рендерится и напрямую, и в будущих местах вызова.
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { AppRoot } from './ui';

import { DeadlineCell, DeadlineHero } from './DeadlineCard';
import type { Deadline } from '../lib/deadlines';

const MSK = 'Europe/Moscow';
const NOW = new Date('2026-09-29T09:00:00.000Z'); // 12:00 MSK

function deadline(dueAt: string, extra: Record<string, unknown> = {}): Deadline {
  return {
    id: 1,
    group_id: null,
    owner_user_id: 1,
    title: 'Кривой',
    description: '',
    due_at: dueAt,
    tz: MSK,
    status: 'active',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    ...extra,
  } as Deadline;
}

function renderCard(node: React.ReactNode) {
  return render(<AppRoot platform="base">{node}</AppRoot>);
}

afterEach(cleanup);

describe('DeadlineCard: битый due_at', () => {
  it('hero показывает прочерк и «срок истёк», а не NaN', () => {
    renderCard(<DeadlineHero deadline={deadline('мусор')} tz={MSK} now={NOW} />);

    expect(screen.getByTestId('hero-due').textContent).toBe('—');
    expect(screen.getByTestId('hero-countdown').textContent).toBe('срок истёк');
    // Битый срок не считается просрочкой — карточка не красится в destructive.
    expect(screen.getByTestId('hero-due').className).not.toContain('overdue');
  });

  it('строка списка показывает прочерк вместо срока и не помечена просроченной', () => {
    renderCard(
      <DeadlineCell deadline={deadline('2026-13-45T99:99:99Z')} tz={MSK} now={NOW} onSelect={() => {}} />,
    );

    expect(screen.getByTestId('cell-1').textContent).toContain('—');
    expect(screen.getByTestId('cell-1').textContent).not.toContain('NaN');
    expect(screen.getByTestId('cell-1').getAttribute('data-overdue')).toBe('false');
  });

  it('валидный срок по-прежнему форматируется и считает просрочку', () => {
    renderCard(<DeadlineHero deadline={deadline('2026-09-29T08:00:00.000Z')} tz={MSK} now={NOW} />);

    expect(screen.getByTestId('hero-due').textContent).toBe('29.09.2026 11:00');
    expect(screen.getByTestId('hero-countdown').textContent).toBe('просрочен на 1 час');
  });
});