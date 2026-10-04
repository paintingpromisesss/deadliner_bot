// Клиентское зеркало серверной нормализации и валидации слага
// (internal/domain/slug.go: Normalize + ValidateStrict). Проверка ДО запроса:
// опечатка не должна тратить счётчик «3 группы в сутки» (спека §3.3).
// Сервер остаётся авторитетом (`slug_invalid`), расхождение правил — не дыра.

/** Границы длины норм-слага в рунах (slug.go: slugMinLen/slugMaxLen). */
export const SLUG_MIN_LEN = 3;
export const SLUG_MAX_LEN = 16;

/** Причина отказа клиентской валидации (тексты — в strings.ts). */
export type SlugError = 'empty' | 'too_short' | 'too_long' | 'charset' | 'no_digit';

/**
 * Нормализация слага ровно как на сервере: trim по пробельным, удаление всех
 * пробелов (включая внутренние), верхний регистр, ё → Ё.
 */
export function normalizeSlug(raw: string): string {
  return raw
    .replace(/^\s+|\s+$/g, '')
    .replace(/ /g, '')
    .toUpperCase()
    .replace(/ё/g, 'Ё');
}

/** Кодпоинты строки (руны, а не UTF-16-единицы: Go считает длину в рунах). */
function runes(value: string): string[] {
  return Array.from(value);
}

/**
 * Валидация НОРМАЛИЗОВАННОГО слага: 3..16 рун, сегменты через «-» непустые,
 * А-Я/A-Z/0-9, минимум одна цифра (М8О-401Б-23). «Ё» тоже отвергается: она
 * вне диапазона А..Я, и хотя Normalize переводит ё в Ё, строгий набор её не
 * пропускает — зеркало обязано вести себя как сервер.
 */
export function validateSlugStrict(normalized: string): SlugError | null {
  const chars = runes(normalized);
  if (chars.length === 0) return 'empty';
  if (chars.length < SLUG_MIN_LEN) return 'too_short';
  if (chars.length > SLUG_MAX_LEN) return 'too_long';

  let hasDigit = false;
  for (const segment of normalized.split('-')) {
    if (segment === '') return 'charset';
    for (const ch of runes(segment)) {
      const ok =
        (ch >= 'А' && ch <= 'Я') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9');
      if (!ok) return 'charset';
      if (ch >= '0' && ch <= '9') hasDigit = true;
    }
  }
  if (!hasDigit) return 'no_digit';
  return null;
}

/**
 * Полный цикл клиентской проверки поля: нормализует (как при отправке) и
 * проверяет. Компонент показывает ошибку по её типу, а в API уходит именно
 * `normalized` — чтобы показанное и отправленное совпадали.
 */
export function checkSlug(raw: string): { normalized: string; error: SlugError | null } {
  const normalized = normalizeSlug(raw);
  return { normalized, error: validateSlugStrict(normalized) };
}

/** Максимальная длина заголовка группы (тот же предел, что у дедлайнов). */
export const GROUP_TITLE_MAX = 200;

/** Причина отказа валидации формы создания группы. */
export type GroupFormError = 'title_required' | 'title_long';

/** Проверка заголовка: пустой (после trim) и длиннее 200 символов — отказ. */
export function validateGroupTitle(title: string): GroupFormError | null {
  const trimmed = title.trim();
  if (trimmed === '') return 'title_required';
  if (runes(trimmed).length > GROUP_TITLE_MAX) return 'title_long';
  return null;
}
