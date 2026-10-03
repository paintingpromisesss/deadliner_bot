import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// base './' — статика отдаётся из embed.FS Go-бинарника за одним origin с API,
// поэтому относительные пути к ассетам безопаснее абсолютных (спека §5.3).
export default defineConfig({
  base: './',
  plugins: [react(), tailwindcss()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      // В dev-режиме API живёт на Go-сервере (localhost:8080); в проде — тот же
      // origin, что и статика.
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
    // jsdom не реализует matchMedia: патчим до тестов (анимации Modal читают
    // prefers-reduced-motion, тема — prefers-color-scheme).
    setupFiles: ['./src/test/setup.ts'],
  },
});
