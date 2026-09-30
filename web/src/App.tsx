import { useEffect, useMemo } from 'react';
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot, List, Placeholder, Section, Spinner } from '@telegram-apps/telegram-ui';

import { getPlatform, hasInitData } from './lib/tma';
import { useAuthStore } from './stores/auth';
import { routeGroupID, routeName, useRoute } from './router';
import { TabBar } from './components/TabBar';
import { CalendarScreen } from './screens/calendar/CalendarScreen';
import { DeadlinesScreen } from './screens/deadlines/DeadlinesScreen';
import { GroupsScreen } from './screens/groups/GroupsScreen';
import { GroupDetailScreen } from './screens/groups/GroupDetailScreen';
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

/** Экраны Task 13–15: settings, дедлайны, календарь, группы и группа детально. */
function Routes() {
  const route = useRoute();
  switch (routeName(route)) {
    case '/calendar':
      return <CalendarScreen />;
    case '/groups': {
      // «#/groups/123» — детали; «#/groups» и мусор в id — список.
      const groupID = routeGroupID(route);
      return groupID === null ? <GroupsScreen /> : <GroupDetailScreen groupID={groupID} />;
    }
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

  // Осознанный выход: нейтральный экран без «ошибки» и без автоматического
  // повторного входа (иначе logout немедленно логинился бы обратно по initData).
  if (status === 'anonymous') {
    return (
      <List>
        <Section>
          <Placeholder
            header="Вы вышли из аккаунта"
            description="Сессия отозвана. Войдите снова, чтобы вернуться к дедлайнам."
            action={
              hasInitData() ? (
                <button type="button" className="dl-retry" onClick={() => void bootstrap()}>
                  Войти снова
                </button>
              ) : undefined
            }
          />
        </Section>
      </List>
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
  // platform статичен (клиент не меняет платформу на ходу) — прокидываем явно.
  const platform = useMemo(() => getPlatform(), []);

  // appearance НЕ передаём: telegram-ui в этом случае сам берёт
  // window.Telegram.WebApp.colorScheme и подписывается на 'themeChanged'
  // (useAppearance.js: при заданном пропе подписки не происходит и смена темы
  // в Telegram игнорируется).
  return (
    <AppRoot platform={platform} className="dl-root">
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