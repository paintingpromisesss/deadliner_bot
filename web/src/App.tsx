import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot, List, Placeholder, Section, Spinner } from './components/ui';

import { hasInitData, getStartParam, syncBackButton } from './lib/tma';
import { useAuthStore } from './stores/auth';
import { navigate, routerBack, routeGroupID, routeName, useRoute } from './router';
import { TabBar } from './components/TabBar';
import { CalendarScreen } from './screens/calendar/CalendarScreen';
import { DeadlinesScreen } from './screens/deadlines/DeadlinesScreen';
import { GroupsScreen } from './screens/groups/GroupsScreen';
import { GroupDetailScreen } from './screens/groups/GroupDetailScreen';
import { JoinGroupSheet } from './components/JoinGroupSheet';
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

/** Экраны по имени маршрута: settings, дедлайны, календарь, группы. */
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

/**
 * startapp-параметр (Main App flow): кнопка приглашения из чата открывает
 * Mini App с кодом инвайта и экраном «Вступить в группу?». Лимит тратится
 * только кнопкой «Вступить». Параметр читается ОДИН раз при монтировании:
 * повторная реакция при ре-рендере снова открывала бы модалку после отмены.
 */
function InviteJoinGate() {
  const [startParam] = useState(() => getStartParam());
  const [open, setOpen] = useState(startParam !== undefined);

  if (startParam === undefined) return null;
  return (
    <JoinGroupSheet
      open={open}
      onOpenChange={setOpen}
      code={startParam}
      onJoined={(group) => navigate(`/groups/${group.id}`)}
    />
  );
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

  // Осознанный выход или отсутствие окружения Telegram: нейтральный экран без
  // «ошибки» и без автоматического повторного входа (иначе logout немедленно
  // логинился бы обратно по initData).
  if (status === 'anonymous') {
    // В обычном браузере initData нет вовсе: кнопка «Войти снова» здесь ничего
    // не исправит, и текст про отозванную сессию был бы неправдой.
    const inTelegram = hasInitData();
    return (
      <List>
        <Section>
          <Placeholder
            header={inTelegram ? 'Вы вышли из аккаунта' : 'Откройте приложение из Telegram'}
            description={
              inTelegram
                ? 'Сессия отозвана. Войдите снова, чтобы вернуться к дедлайнам.'
                : 'Вход выполняется автоматически по данным Telegram — из браузера он невозможен.'
            }
            action={
              inTelegram ? (
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

/** Синхронизация нативной кнопки «Назад» (5.1). */
function BackButtonSync() {
  const route = useRoute();
  const isRoot = route.raw === '#/' || route.path.length === 0;

  useEffect(() => {
    return syncBackButton(isRoot, routerBack);
  }, [isRoot]);

  return null;
}

export function App() {
  return (
    <AppRoot>
      <QueryClientProvider client={queryClient}>
        <AuthGate>
          <BackButtonSync />
          <main className="dl-main">
            <Routes />
          </main>
          <InviteJoinGate />
          <TabBar />
        </AuthGate>
      </QueryClientProvider>
    </AppRoot>
  );
}
