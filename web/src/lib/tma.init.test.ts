// Ре-энтрантность initTMA: StrictMode/HMR вызывают initTMA повторно, и без
// гварда viewport.mount() уходил бы в SDK второй раз (ConcurrentCallError).
// SDK замокан — проверяются именно вызовы, а не окружение.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdk = vi.hoisted(() => {
  const restore = vi.fn();
  const mount = Object.assign(vi.fn(), { isAvailable: () => true });
  const expand = Object.assign(vi.fn(), { isAvailable: () => true });
  return { restore, mount, expand };
});

vi.mock('@telegram-apps/sdk-react', () => ({
  isTMA: () => true,
  initData: {
    restore: () => sdk.restore(),
    raw: () => 'query_id=1&hash=abc',
  },
  viewport: { mount: sdk.mount, expand: sdk.expand },
  themeParams: { isDark: () => false },
  hapticFeedback: {
    impactOccurred: Object.assign(() => {}, { isAvailable: () => true }),
    notificationOccurred: Object.assign(() => {}, { isAvailable: () => true }),
  },
  retrieveLaunchParams: () => ({ tgWebAppPlatform: 'android' }),
}));

import { initTMA, resetTMAInit } from './tma';

const { restore, mount, expand } = sdk;

beforeEach(() => {
  resetTMAInit();
  restore.mockClear();
  mount.mockClear();
  expand.mockClear();
});

describe('initTMA идемпотентна', () => {
  it('повторный вызов не перезапускает restore/mount/expand', () => {
    initTMA();
    initTMA();
    initTMA();

    expect(restore).toHaveBeenCalledTimes(1);
    expect(mount).toHaveBeenCalledTimes(1);
    expect(expand).toHaveBeenCalledTimes(1);
  });

  it('resetTMAInit разрешает повторную инициализацию (для тестов/HMR)', () => {
    initTMA();
    resetTMAInit();
    initTMA();

    expect(restore).toHaveBeenCalledTimes(2);
    expect(mount).toHaveBeenCalledTimes(2);
  });
});
