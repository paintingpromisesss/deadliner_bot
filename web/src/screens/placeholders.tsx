// Экран-заглушка (Task 13): настоящий список групп — задача 15. Здесь только
// каркас навигации, чтобы роутер и таб-бар были проверяемы.
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

export function GroupsScreen() {
  return <StubScreen title="Группы" />;
}