// Экраны-заглушки (Task 13): настоящие списки дедлайнов, календарь и группы —
// задачи 14–15. Здесь только каркас навигации, чтобы роутер и таб-бар были
// проверяемы.
import { Screen } from '../components/Screen';

interface StubProps {
  title: string;
}

/** Заглушка экрана: заголовок + пояснение «в разработке». */
export function StubScreen({ title }: StubProps) {
  return (
    <Screen title={title}>
      <p className="dl-stub" data-testid="stub">
        В разработке
      </p>
    </Screen>
  );
}

export function DeadlinesScreen() {
  return (
    <Screen title="Дедлайны">
      <p className="dl-stub" data-testid="stub">
        В разработке
      </p>
    </Screen>
  );
}

export function CalendarScreen() {
  return <StubScreen title="Календарь" />;
}

export function GroupsScreen() {
  return <StubScreen title="Группы" />;
}