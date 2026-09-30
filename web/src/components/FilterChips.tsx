// Чип-фильтр на базе TelegramUI Chip (Blocks → Form/Chip). Chip рендерит
// div/Tappable, поэтому для доступности передаём Component="button": фильтр —
// это действие, а не декорация, и должен быть фокусируемым с клавиатуры.
import { Chip } from '@telegram-apps/telegram-ui';
import { hapticImpact } from '../lib/tma';

export interface FilterOption<T extends string> {
  value: T;
  label: string;
}

interface FilterChipsProps<T extends string> {
  options: readonly FilterOption<T>[];
  value: T;
  onChange: (value: T) => void;
  /** aria-label группы чипов. */
  label: string;
}

/** Горизонтальная группа чипов-переключателей (спека §9: «Все/Личные/Групповые»). */
export function FilterChips<T extends string>({
  options,
  value,
  onChange,
  label,
}: FilterChipsProps<T>) {
  return (
    <div className="dl-chips" role="group" aria-label={label}>
      {options.map((option) => {
        const selected = option.value === value;
        return (
          <Chip
            key={option.value}
            Component="button"
            type="button"
            mode={selected ? 'elevated' : 'mono'}
            className="dl-chip"
            aria-pressed={selected}
            data-selected={selected ? 'true' : 'false'}
            onClick={() => {
              hapticImpact('light');
              onChange(option.value);
            }}
          >
            {option.label}
          </Chip>
        );
      })}
    </div>
  );
}

interface PresetChipsProps {
  options: readonly { minutes: number; label: string }[];
  selected: readonly number[];
  onToggle: (minutes: number) => void;
  /** aria-label группы. */
  label: string;
  disabled?: boolean;
}

/** Чипы-пресеты напоминаний (7д/3д/24ч), переключаемые (спека §9, экран 4).
 *
 * Рисуются тем же Chip, что и фильтры: отличие только в неподсвеченном режиме
 * (outline) и в haptic-отклике на нажатие — форма и список пользуются одним
 * примитивом, а не двумя похожими разметками.
 */
export function PresetChips({
  options,
  selected,
  onToggle,
  label,
  disabled = false,
}: PresetChipsProps) {
  return (
    <div className="dl-chips" role="group" aria-label={label}>
      {options.map((option) => {
        const on = selected.includes(option.minutes);
        return (
          <Chip
            key={option.minutes}
            Component="button"
            type="button"
            mode={on ? 'elevated' : 'outline'}
            className="dl-chip"
            aria-pressed={on}
            data-selected={on ? 'true' : 'false'}
            data-testid={`preset-${option.minutes}`}
            disabled={disabled}
            onClick={() => {
              hapticImpact('light');
              onToggle(option.minutes);
            }}
          >
            {option.label}
          </Chip>
        );
      })}
    </div>
  );
}