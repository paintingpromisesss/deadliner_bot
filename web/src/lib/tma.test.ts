// Браузерная безопасность tma.ts: вне Telegram (jsdom без window.Telegram)
// ни один вызов SDK не должен бросать — иначе dev-режим и пререндер падают.
// Это контракт из брифа Task 13: «MUST work in a plain browser».
import { describe, expect, it } from 'vitest';
import {
  getAppearance,
  getInitData,
  getPlatform,
  hasInitData,
  hapticImpact,
  hapticNotification,
  initTMA,
  platformFromLaunchParams,
} from './tma';

describe('tma.ts вне Telegram', () => {
  it('initTMA не бросает и не инициализирует SDK', () => {
    expect(() => initTMA()).not.toThrow();
  });

  it('initData недоступен → undefined, hasInitData false', () => {
    expect(getInitData()).toBeUndefined();
    expect(hasInitData()).toBe(false);
  });

  it('платформа и тема имеют безопасные фолбэки', () => {
    expect(getPlatform()).toBe('base');
    expect(['light', 'dark']).toContain(getAppearance());
  });

  it('haptics — тихий no-op', () => {
    expect(() => hapticImpact('medium')).not.toThrow();
    expect(() => hapticNotification('error')).not.toThrow();
  });
});

describe('platformFromLaunchParams', () => {
  it('iOS-клиент → ios (влияет на визуальный стиль TelegramUI)', () => {
    expect(platformFromLaunchParams('ios')).toBe('ios');
    expect(platformFromLaunchParams('macos')).toBe('ios');
  });

  it('остальные платформы и undefined → base', () => {
    expect(platformFromLaunchParams('android')).toBe('base');
    expect(platformFromLaunchParams('tdesktop')).toBe('base');
    expect(platformFromLaunchParams(undefined)).toBe('base');
  });
});