// Инициализация Telegram Mini Apps SDK и безопасные обёртки над ним.
//
// Главное требование: приложение обязано работать и в обычном браузере (dev
// без Telegram). Поэтому каждый вызов SDK проходит через проверку доступности
// (`initData.restore`, `expand.isAvailable()` и т.п.), а `window.Telegram`
// читается только через SDK — прямых обращений нет.
import {
  hapticFeedback,
  initData,
  isTMA,
  retrieveLaunchParams,
  themeParams,
  viewport,
} from '@telegram-apps/sdk-react';

export type Appearance = 'light' | 'dark';
/** TelegramUI AppRoot понимает только эти два значения. */
export type TGPlatform = 'base' | 'ios';
export type WaveStyle = 'light' | 'medium' | 'heavy' | 'rigid' | 'soft';
export type NotifyType = 'error' | 'success' | 'warning';

type LaunchParamsCamel = {
  tgWebAppPlatform?: string;
  tgWebAppData?: unknown;
};

/** Платформа из launch params: iOS-клиент → 'ios', всё остальное → 'base'. */
export function platformFromLaunchParams(platform: string | undefined): TGPlatform {
  return platform === 'ios' || platform === 'macos' ? 'ios' : 'base';
}

/**
 * Инициализация SDK. Возвращает launch params (или пустой объект вне Telegram).
 * Идемпотентна: вызывается один раз из main.tsx.
 */
export function initTMA(): void {
  if (!isTMA()) {
    // Вне Telegram SDK не инициализируется — все дальнейшие вызовы
    // отфильтрованы guard'ами isAvailable()/isMounted().
    return;
  }
  try {
    initData.restore();
  } catch {
    // launch params могут отсутствовать — приложение стартует в dev-режиме.
  }
  try {
    if (viewport.mount.isAvailable()) viewport.mount();
    if (viewport.expand.isAvailable()) viewport.expand();
  } catch {
    // viewport недоступен — не критично.
  }
}

/** Raw initData для POST /api/v1/auth/telegram. undefined вне Telegram. */
export function getInitData(): string | undefined {
  if (!isTMA()) return undefined;
  try {
    return initData.raw();
  } catch {
    return undefined;
  }
}

/** true, если приложение открыто внутри Telegram с валидным initData. */
export function hasInitData(): boolean {
  const raw = getInitData();
  return typeof raw === 'string' && raw.length > 0;
}

/** Платформа клиента для TelegramUI AppRoot. */
export function getPlatform(): TGPlatform {
  if (!isTMA()) return 'base';
  try {
    const lp = retrieveLaunchParams(true) as LaunchParamsCamel;
    return platformFromLaunchParams(lp.tgWebAppPlatform);
  } catch {
    return 'base';
  }
}

/** Тема оформления: из themeParams, с фолбэком на системную. */
export function getAppearance(): Appearance {
  if (isTMA()) {
    try {
      if (themeParams.isDark()) return 'dark';
    } catch {
      // themeParams не смонтированы — падаем на prefers-color-scheme.
    }
  }
  if (typeof window !== 'undefined' && window.matchMedia) {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  return 'light';
}

/** Тактильный отклик. Вне Telegram — no-op. */
export function hapticImpact(style: WaveStyle = 'light'): void {
  if (!isTMA()) return;
  try {
    if (hapticFeedback.impactOccurred.isAvailable()) hapticFeedback.impactOccurred(style);
  } catch {
    // haptics не поддерживаются — молча игнорируем.
  }
}

/** Haptic-нотификация (успех/ошибка/предупреждение). Вне Telegram — no-op. */
export function hapticNotification(type: NotifyType): void {
  if (!isTMA()) return;
  try {
    if (hapticFeedback.notificationOccurred.isAvailable()) hapticFeedback.notificationOccurred(type);
  } catch {
    // haptics не поддерживаются — молча игнорируем.
  }
}