import { useEffect, useMemo } from 'react';
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot, List, Placeholder, Section, Spinner } from '@telegram-apps/telegram-ui';

import { getAppearance, getPlatform, hasInitData } from './lib/tma';
import { useAuthStore } from './stores/auth';
import { routeName, useRoute } from './router';
import { TabBar } from './components/TabBar';
import { CalendarScreen, DeadlinesScreen, GroupsScreen } from './screens/placeholders';
import { SettingsScreen } from './screens/SettingsScreen';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    },
  },
});

/** Экраны Task 13: рабочий settings + заглушки под задачи 14–15. */
function Routes() {
  const route = useRoute();
  switch (routeName(route)) {
    case '/calendar':
      return <CalendarScreen />;
    case '/groups':
      return <GroupsScreen />;
    case '/settings':
      return <SettingsScreen />;
    default:
      return <DeadlinesScreen />;
  }
}

/** Гейт авторизации: спиннер на bootstrap, экран ошибки при провале входа. */
function AuthGate({ children }: { children: ReactNode }) {
  const status = useAuthStore((s) => s.status);
  const error = useAuthStore((s) => s.error);
  const bootstrap = useAuthStore((s) => s.bootstrap);

  useEffect(() => {
    void bootstrap();
  }, [bootstrap]);

  if (status === 'init') {
    return (
      <div className="dl-centered" data-testid="auth-spinner">
        <Spinner size="l" />
      </div>
    );
  }

  if (status === 'error') {
    return (
      <List>
        <Section>
          <Placeholder
            header="Не удалось войти"
            description={
              hasInitData()
                ? error ?? 'Повторная авторизация не прошла. Закройте и откройте Mini App заново.'
                : 'Откройте приложение из Telegram — вход выполняется автоматически по initData.'
            }
            action={
              hasInitData() ? (
                <button type="button" className="dl-retry" onClick={() => void bootstrap()}>
                  Повторить
                </button>
              ) : undefined
            }
          />
        </Section>
      </List>
    );
  }

  return <>{children}</>;
}

export function App() {
  const platform = useMemo(() => getPlatform(), []);
  const appearance = useMemo(() => getAppearance(), []);

  return (
    <AppRoot platform={platform} appearance={appearance} className="dl-root">
      <QueryClientProvider client={queryClient}>
        <AuthGate>
          <main className="dl-main">
            <Routes />
          </main>
          <TabBar />
        </AuthGate>
      </QueryClientProvider>
    </AppRoot>
  );
}