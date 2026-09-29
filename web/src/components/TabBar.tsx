// Нижний таб-бар (спека §9: «Дедлайны / Календарь / Группы / Настройки»).
// Собственная реализация вместо TelegramUI Tabbar: не тянем иконки и
// FixedLayout-контекст, зато получаем предсказуемое поведение навигации и
// haptic-отклик. Иконки — инлайновые SVG (без библиотеки иконок).
import { navigate, routeName, useRoute } from '../router';
import { hapticImpact } from '../lib/tma';

interface TabDef {
  name: string;
  to: string;
  label: string;
  icon: JSX.Element;
}

const iconProps = {
  width: 24,
  height: 24,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
  'aria-hidden': true,
};

const TABS: TabDef[] = [
  {
    name: '/',
    to: '/',
    label: 'Дедлайны',
    icon: (
      <svg {...iconProps}>
        <path d="M8 3v3M16 3v3M4 8h16M5 5h14a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1Z" />
        <path d="M8 13h3" />
      </svg>
    ),
  },
  {
    name: '/calendar',
    to: '/calendar',
    label: 'Календарь',
    icon: (
      <svg {...iconProps}>
        <rect x="3.5" y="5" width="17" height="15" rx="2" />
        <path d="M3.5 9.5h17M8 3.5v3M16 3.5v3" />
      </svg>
    ),
  },
  {
    name: '/groups',
    to: '/groups',
    label: 'Группы',
    icon: (
      <svg {...iconProps}>
        <circle cx="9" cy="9" r="3" />
        <path d="M3.5 19c0-2.5 2.5-4 5.5-4s5.5 1.5 5.5 4" />
        <path d="M16 7.5a2.7 2.7 0 0 1 0 5M17.5 15.5c1.8.4 3 1.6 3 3.5" />
      </svg>
    ),
  },
  {
    name: '/settings',
    to: '/settings',
    label: 'Настройки',
    icon: (
      <svg {...iconProps}>
        <circle cx="12" cy="12" r="3" />
        <path d="M12 3.5v2M12 18.5v2M3.5 12h2M18.5 12h2M6 6l1.4 1.4M16.6 16.6 18 18M18 6l-1.4 1.4M7.4 16.6 6 18" />
      </svg>
    ),
  },
];

export function TabBar() {
  const route = useRoute();
  const active = routeName(route);

  return (
    <nav className="dl-tabbar" aria-label="Основная навигация">
      {TABS.map((tab) => (
        <button
          key={tab.name}
          type="button"
          className="dl-tabbar__item"
          aria-current={active === tab.name ? 'page' : undefined}
          data-selected={active === tab.name ? 'true' : 'false'}
          onClick={() => {
            hapticImpact('light');
            navigate(tab.to);
          }}
        >
          <span className="dl-tabbar__icon">{tab.icon}</span>
          <span className="dl-tabbar__label">{tab.label}</span>
        </button>
      ))}
    </nav>
  );
}