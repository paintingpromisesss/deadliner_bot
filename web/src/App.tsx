import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AppRoot, List, Placeholder, Section, Spinner } from './components/ui';

import { hasInitData } from './lib/tma';
import { getStartParam } from './lib/tma';
import { useAuthStore } from './stores/auth';
import { routeGroupID, routeName, useRoute } from './router';
import { TabBar } from './components/TabBar';
import { CalendarScreen } from './screens/calendar/CalendarScreen';
import { DeadlinesScreen } from './screens/deadlines/DeadlinesScreen';
import { GroupsScreen } from './screens/groups/GroupsScreen';
import { GroupDetailScreen } from './screens/groups/GroupDetailScreen';
import { JoinGroupSheet } from './components/JoinGroupSheet';
import { navigate } from './router';
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

/**
 * Обработка startapp-параметра (Main App flow): Mini App, открытое кнопкой
 * приглашения из чата группы, получает код инвайта и показывает экран
 * подтверждения «Вступить в группу?». Лимит инвайта тратится только кнопкой
 * «Вступить»; закрытие окна или «Отмена» ничего не списывают.
 *
 * Параметр читается ОДИН раз при монтировании: повторная реакция на тот же
 * код при ре-рендере снова открывала бы модалку после отмены.
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

export function App() {
  return (
    <AppRoot>
      <QueryClientProvider client={queryClient}>
        <AuthGate>
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
