// Тесты управления Telegram MainButton: кнопка монтируется, получает текст и
// флаг доступности, а cleanup снимает слушатель и прячет кнопку.
//
// SDK замокан (как в tma.init.test.ts): вне Telegram эта ветка кода не
// выполняется, а её контракт — «после закрытия формы клик не должен отправлять
// старый сабмит» — критичен.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdk = vi.hoisted(() => {
  const setParams = vi.fn<(params: unknown) => void>();
  const offClick = vi.fn<() => void>();
  const onClick = vi.fn<(fn: () => void) => () => void>(() => offClick);
  const isMounted = vi.fn(() => false);
  const mount = Object.assign(vi.fn(), { isAvailable: () => true });
  return { setParams, onClick, offClick, isMounted, mount };
});

vi.mock('@telegram-apps/sdk-react', () => ({
  isTMA: () => true,
  initData: { restore: () => {}, raw: () => 'query_id=1&hash=abc' },
  viewport: {
    mount: Object.assign(() => {}, { isAvailable: () => false }),
    expand: Object.assign(() => {}, { isAvailable: () => false }),
  },
  themeParams: { isDark: () => false },
  hapticFeedback: {
    impactOccurred: Object.assign(() => {}, { isAvailable: () => false }),
    notificationOccurred: Object.assign(() => {}, { isAvailable: () => false }),
  },
  retrieveLaunchParams: () => ({ tgWebAppPlatform: 'android' }),
  mainButton: {
    mount: sdk.mount,
    isMounted: () => sdk.isMounted(),
    onClick: (fn: () => void) => sdk.onClick(fn),
    setParams: (params: unknown) => sdk.setParams(params),
  },
}));

import { isMainButtonAvailable, showMainButton } from './tma';

beforeEach(() => {
  sdk.setParams.mockClear();
  sdk.onClick.mockClear();
  sdk.offClick.mockClear();
  sdk.mount.mockClear();
  sdk.isMounted.mockReturnValue(false);
});

describe('showMainButton', () => {
  it('монтирует кнопку, показывает текст и подписывается на клик', () => {
    const click = vi.fn();
    showMainButton({ text: 'Создать', enabled: true, onClick: click });

    expect(sdk.mount).toHaveBeenCalledTimes(1);
    expect(sdk.onClick).toHaveBeenCalledWith(click);
    expect(sdk.setParams).toHaveBeenCalledWith({
      text: 'Создать',
      isVisible: true,
      isEnabled: true,
      isLoaderVisible: false,
      hasShineEffect: true,
    });
  });

  it('не монтирует кнопку повторно, если она уже смонтирована', () => {
    sdk.isMounted.mockReturnValue(true);
    showMainButton({ text: 'Сохранить', onClick: () => {} });
    expect(sdk.mount).not.toHaveBeenCalled();
  });

  it('cleanup снимает слушатель и прячет кнопку (иначе клик отправил бы старый сабмит)', () => {
    const cleanup = showMainButton({ text: 'Создать', onClick: () => {} });
    cleanup();

    expect(sdk.offClick).toHaveBeenCalledTimes(1);
    expect(sdk.setParams).toHaveBeenLastCalledWith({ isVisible: false, isLoaderVisible: false });
  });

  it('isMainButtonAvailable отражает возможность монтирования', () => {
    expect(isMainButtonAvailable()).toBe(true);
  });

  it('disabled и loading прокидываются в состояние кнопки', () => {
    showMainButton({ text: 'Создать', enabled: false, loading: true, onClick: () => {} });
    expect(sdk.setParams).toHaveBeenCalledWith(
      expect.objectContaining({ isEnabled: false, isLoaderVisible: true }),
    );
  });
});