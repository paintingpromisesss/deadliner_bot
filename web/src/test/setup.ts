// Полифилл matchMedia для jsdom: тема и анимации читают медиа-запросы
// (prefers-color-scheme, prefers-reduced-motion), которых jsdom не имеет.
// Тесты, которым нужен конкретный результат, перезаписывают через
// vi.stubGlobal('matchMedia', ...) — App.theme.test.tsx так и делает.
if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}
