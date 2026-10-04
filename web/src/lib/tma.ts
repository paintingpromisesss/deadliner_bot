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
  mainButton,
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
  tgWebAppStartParam?: string;
};

/** Платформа из launch params: iOS-клиент → 'ios', всё остальное → 'base'. */
export function platformFromLaunchParams(platform: string | undefined): TGPlatform {
  return platform === 'ios' || platform === 'macos' ? 'ios' : 'base';
}

/**
 * Инициализация SDK. Идемпотентна: повторный вызов (StrictMode, HMR,
 * повторный маунт) не перезапускает viewport.mount — SDK бросает
 * ConcurrentCallError на параллельный маунт, а расширение viewport второй раз
 * бессмысленно.
 */
let tmaInitialized = false;

export function initTMA(): void {
  if (tmaInitialized) return;
  if (!isTMA()) {
    // Вне Telegram SDK не инициализируется — все дальнейшие вызовы
    // отфильтрованы guard'ами isAvailable()/isMounted(). Флаг не выставляем:
    // в dev-режиме страница может оказаться в Telegram после перезагрузки.
    return;
  }
  tmaInitialized = true;
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

/** Сброс флага инициализации. Только для тестов. */
export function resetTMAInit(): void {
  tmaInitialized = false;
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

/**
 * startapp-параметр (tgWebAppStartParam) — передаётся боту в Main App-кнопке
 * инвайта: код инвайта, по которому Mini App открывает экран подтверждения
 * «Вступить в группу?». undefined вне Telegram или без параметра.
 */
export function getStartParam(): string | undefined {
  if (!isTMA()) return undefined;
  try {
    const lp = retrieveLaunchParams(true) as LaunchParamsCamel;
    const v = lp.tgWebAppStartParam;
    return typeof v === 'string' && v.length > 0 ? v : undefined;
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

/**
 * Telegram MainButton доступна для управления. Вне Telegram (dev-браузер) —
 * false, и форма обязана показать собственную кнопку submit: без неё создать
 * дедлайн было бы нечем.
 */
export function isMainButtonAvailable(): boolean {
  if (!isTMA()) return false;
  try {
    return mainButton.mount.isAvailable();
  } catch {
    return false;
  }
}

interface MainButtonOptions {
  text: string;
  /** Включена ли кнопка; false → клики не приходят, кнопка «серая». */
  enabled?: boolean;
  loading?: boolean;
  onClick: () => void;
}

/**
 * Показать MainButton и подписаться на клик; возвращает функцию очистки
 * (спрятать кнопку, снять слушатель). Возвращаем cleanup, а не пару
 * show/hide, чтобы вызывающий не мог забыть снять слушателя — иначе после
 * закрытия формы клик по невидимой кнопке отправлял бы старый сабмит.
 */
export function showMainButton({
  text,
  enabled = true,
  loading = false,
  onClick,
}: MainButtonOptions): () => void {
  if (!isMainButtonAvailable()) return () => {};

  try {
    if (!mainButton.isMounted()) mainButton.mount();
    const off = mainButton.onClick(onClick);
    mainButton.setParams({
      text,
      isVisible: true,
      isEnabled: enabled,
      isLoaderVisible: loading,
      // Блик на кнопке — как у системных CTA в клиентах Telegram.
      hasShineEffect: true,
    });
    return () => {
      off();
      try {
        mainButton.setParams({ isVisible: false, isLoaderVisible: false });
      } catch {
        // Кнопка могла быть размонтирована (закрытие клиента) — не критично.
      }
    };
  } catch {
    // SDK в неожиданном состоянии: форма остаётся с собственной кнопкой.
    return () => {};
  }
}
