import { afterEach, describe, expect, it, vi } from 'vitest';
import { navigate, parseHash, routeName } from './router';

afterEach(() => {
  window.location.hash = '';
});

describe('parseHash', () => {
  it('разбирает путь и игнорирует пустые сегменты', () => {
    expect(parseHash('#/groups/123').path).toEqual(['groups', '123']);
    expect(parseHash('#//groups//123/').path).toEqual(['groups', '123']);
  });

  it('разбирает query внутри хеша с декодированием', () => {
    const route = parseHash('#/groups?q=%D0%98%D0%9A%D0%91%D0%9E&page=2');
    expect(route.path).toEqual(['groups']);
    expect(route.query).toEqual({ q: 'ИКБО', page: '2' });
  });

  it('пустой хеш — корень', () => {
    expect(parseHash('').path).toEqual([]);
    expect(parseHash('#/').path).toEqual([]);
  });

  it('принимает хеш без решётки и параметр без значения', () => {
    expect(parseHash('/settings?flag').path).toEqual(['settings']);
    expect(parseHash('/settings?flag').query).toEqual({ flag: '' });
  });

  it('битая percent-последовательность не бросает URIError, а отдаёт сырое значение', () => {
    // decodeURIComponent('100%') кидает URIError; хеш приходит из адресной
    // строки, и падать на разборе маршрута нельзя.
    expect(() => parseHash('#/groups?q=100%')).not.toThrow();
    expect(parseHash('#/groups?q=100%').query).toEqual({ q: '100%' });
    expect(parseHash('#/groups?%zz=1').query).toEqual({ '%zz': '1' });

    // Валидное кодирование продолжает раскрываться.
    expect(parseHash('#/groups?q=%D0%98%D0%9A%D0%91%D0%9E').query).toEqual({ q: 'ИКБО' });
  });
});

describe('routeName', () => {
  it('маппит первый сегмент на таб', () => {
    expect(routeName(parseHash('#/'))).toBe('/');
    expect(routeName(parseHash('#/calendar'))).toBe('/calendar');
    expect(routeName(parseHash('#/groups/42'))).toBe('/groups');
    expect(routeName(parseHash('#/settings'))).toBe('/settings');
  });

  it('неизвестный маршрут — главный экран', () => {
    expect(routeName(parseHash('#/nope/1'))).toBe('/');
  });
});

describe('navigate', () => {
  it('меняет location.hash без перезагрузки', () => {
    const pushState = vi.spyOn(window.history, 'pushState');
    navigate('/settings');
    expect(window.location.hash).toBe('#/settings');

    navigate('groups/7');
    expect(window.location.hash).toBe('#/groups/7');
    // Хеш-навигация не трогает history API вручную.
    expect(pushState).not.toHaveBeenCalled();
  });

  it('одинаковый путь не переписывает hash повторно', () => {
    navigate('/calendar');
    const before = window.location.href;
    navigate('/calendar');
    expect(window.location.href).toBe(before);
  });
});