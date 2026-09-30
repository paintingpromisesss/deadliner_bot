// Аватар группы: круг с началом слага (спека §9, экран 5 — «аватар-слаг +
// роль-бейдж»). Слаг — это и есть визуальная идентичность группы (М8О-401Б-23),
// поэтому берём первые два значимых символа: длинный номер в кружок не влезает.
import { strings } from '../lib/strings';

/** Первые два символа слага (битый/пустой слаг → прочерк из каталога). */
export function avatarText(slug: string): string {
  const trimmed = slug.trim();
  return trimmed ? Array.from(trimmed).slice(0, 2).join('') : strings.common.unknown;
}

interface SlugAvatarProps {
  slug: string;
}

export function SlugAvatar({ slug }: SlugAvatarProps) {
  return (
    <span className="dl-avatar" aria-hidden="true" data-testid="slug-avatar">
      {avatarText(slug)}
    </span>
  );
}