// Чипы-фильтры и пресеты (спека §9: «Все/Личные/Групповые» и 7д/3д/24ч).
// Собственная реализация по дизайн-системе макета: кнопка-чип с состоянием
// selected/on; фильтр — «залитый» активный чип, пресет — контурный включённый.
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
          <button
            key={option.value}
            type="button"
            className="dl-chip"
            aria-pressed={selected}
            data-selected={selected ? 'true' : 'false'}
            onClick={() => {
              hapticImpact('light');
              onChange(option.value);
            }}
          >
            {option.label}
          </button>
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

/** Чипы-пресеты напоминаний (7д/3д/24ч), переключаемые (спeca §9, экран 4).
 *
 * Включённый пресет — контурный чип-«on» (не «залитый», как фильтр): в макете
 * это два разных состояния одного примитива.
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
          <button
            key={option.minutes}
            type="button"
            className={['dl-chip', on ? 'dl-chip--on' : ''].filter(Boolean).join(' ')}
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
          </button>
        );
      })}
    </div>
  );
}
