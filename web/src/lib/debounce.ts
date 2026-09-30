// Дебаунс значения: поиск по слагу шлёт запрос на каждый введённый символ, а
// серверный поиск — это LIKE по всей таблице групп. 300 мс — компромисс между
// «не мигать результатами» и «не ждать после паузы».
import { useEffect, useState } from 'react';

export const DEFAULT_DEBOUNCE_MS = 300;

/**
 * Возвращает значение, обновившееся только после `delay` мс тишины. Смена
 * задержки на лету перезапускает отсчёт (значение ещё не «устоялось»).
 */
export function useDebounced<T>(value: T, delay: number = DEFAULT_DEBOUNCE_MS): T {
  const [settled, setSettled] = useState(value);

  useEffect(() => {
    const id = window.setTimeout(() => setSettled(value), delay);
    return () => window.clearTimeout(id);
  }, [value, delay]);

  return settled;
}