// Минимальный хеш-роутер (спека §9: «#/groups/123»). Никаких зависимостей:
// разбор location.hash, подписка на hashchange, imperative navigate.
import { useEffect, useState } from 'react';

/** Разобранный маршрут: «#/groups/123?tab=x» → path=['groups','123'], query={tab:'x'}. */
export interface Route {
  /** Сегменты пути без решётки и пустых частей. */
  path: string[];
  /** Параметры строки запроса внутри хеша. */
  query: Record<string, string>;
  /** Исходный хеш (для отладки/сравнений). */
  raw: string;
}

export const ROUTES = ['/', '/calendar', '/groups', '/settings'] as const;
export type RouteName = (typeof ROUTES)[number];

/** Разбирает хеш (принимает с решёткой и без) в структуру Route. */
export function parseHash(hash: string): Route {
  const raw = hash || '';
  const withoutHash = raw.startsWith('#') ? raw.slice(1) : raw;
  const [pathPart = '', queryPart = ''] = withoutHash.split('?');
  const path = pathPart.split('/').filter((s) => s.length > 0);
  const query: Record<string, string> = {};
  if (queryPart) {
    for (const pair of queryPart.split('&')) {
      if (!pair) continue;
      const [k, v = ''] = pair.split('=');
      if (k) query[safeDecode(k)] = safeDecode(v);
    }
  }
  return { path, query, raw: raw || '#/' };
}

/**
 * decodeURIComponent бросает URIError на битой последовательности («%»,
 * «%zz»): такой хеш приходит из адресной строки, и падать на разборе маршрута
 * нельзя — показываем сырое значение.
 */
function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

/** «Первый сегмент» маршрута → имя таба; неизвестное → главный экран. */
export function routeName(route: Route): RouteName {
  const head = route.path[0];
  switch (head) {
    case undefined:
      return '/';
    case 'calendar':
      return '/calendar';
    case 'groups':
      return '/groups';
    case 'settings':
      return '/settings';
    default:
      return '/';
  }
}

/**
 * Числовой id группы из маршрута «#/groups/123» → 123; иначе null.
 *
 * Мусор («#/groups/abc», «#/groups/0») даёт null, а не исключение: хеш приходит
 * из адресной строки и редактируется руками — падать на разборе нельзя, экран
 * покажет список групп.
 */
export function routeGroupID(route: Route): number | null {
  if (route.path[0] !== 'groups') return null;
  const raw = route.path[1];
  if (raw === undefined) return null;
  if (!/^[0-9]+$/.test(raw)) return null;
  const id = Number(raw);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}

/** Навигация без перезагрузки: меняет location.hash (событие подхватит роутер). */
export function navigate(path: string): void {
  const clean = path.startsWith('/') ? path : `/${path}`;
  const next = `#${clean}`;
  if (window.location.hash !== next) window.location.hash = next;
}

/** Текущий маршрут с подпиской на hashchange. */
export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parseHash(window.location.hash));

  useEffect(() => {
    const onChange = () => setRoute(parseHash(window.location.hash));
    window.addEventListener('hashchange', onChange);
    // Нормализуем адрес: без хеша показываем главный экран.
    if (!window.location.hash) navigate('/');
    return () => window.removeEventListener('hashchange', onChange);
  }, []);

  return route;
}