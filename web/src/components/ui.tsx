// Собственный UI-слой вместо @telegram-apps/telegram-ui.
//
// Дизайн-система — из экспорта Open Design (deadliner-mini-app.html): токены
// themeParams через CSS-переменные --tg-theme-*, секционные ячейки, чипы,
// bottom sheets, тёмная/светлая темы. Все примитивы — тонкие обёртки над
// нативными элементами (button/input/select/label), чтобы сохранить
// семантику и клавиатурную доступность без библиотеки.
import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

/* ─── Тема ──────────────────────────────────────────────────────────────── */

type Scheme = 'light' | 'dark';

interface WebAppLike {
  colorScheme?: string;
  themeParams?: Record<string, string>;
  onEvent?: (event: string, handler: () => void) => void;
  offEvent?: (event: string, handler: () => void) => void;
}

/** window.Telegram.WebApp без прямых обращений в остальном коде. */
function getWebApp(): WebAppLike | undefined {
  const tg = (window as { Telegram?: { WebApp?: WebAppLike } }).Telegram;
  return tg?.WebApp;
}

function systemScheme(): Scheme {
  if (typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches) {
    return 'dark';
  }
  return 'light';
}

/** camelCase-ключ themeParams → snake_case CSS-переменной (bgColor → bg_color). */
function themeCssVars(params: Record<string, string>): Record<string, string> {
  const vars: Record<string, string> = {};
  for (const [key, value] of Object.entries(params)) {
    if (typeof value === 'string' && value) {
      vars[`--tg-theme-${key.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`)}`] = value;
    }
  }
  return vars;
}

/**
 * Тема оформления: подписка на themeChanged клиента Telegram и на системную
 * схему вне Telegram. Возвращает { scheme, cssVars }: класс переключает набор
 * дефолтных токенов, cssVars (если клиент прислал themeParams) перекрывают
 * их пользовательской темой.
 */
function useTelegramTheme(): { scheme: Scheme; cssVars: Record<string, string> } {
  const [scheme, setScheme] = useState<Scheme>(() => {
    const colorScheme = getWebApp()?.colorScheme;
    return colorScheme === 'dark' || colorScheme === 'light' ? colorScheme : systemScheme();
  });
  const [cssVars, setCssVars] = useState<Record<string, string>>(() => {
    const params = getWebApp()?.themeParams;
    return params ? themeCssVars(params) : {};
  });

  useEffect(() => {
    const webApp = getWebApp();
    // Тема клиента Telegram меняется на ходу: применяем и новую схему, и
    // сами параметры (пользовательская тема ≠ дефолтные токены).
    const onThemeChanged = () => {
      const next = getWebApp();
      const colorScheme = next?.colorScheme;
      if (colorScheme === 'dark' || colorScheme === 'light') setScheme(colorScheme);
      if (next?.themeParams) setCssVars(themeCssVars(next.themeParams));
    };
    // Системная схема — для работы вне Telegram (dev-браузер).
    const media = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
    const onMedia = () => {
      const colorScheme = getWebApp()?.colorScheme;
      if (colorScheme !== 'dark' && colorScheme !== 'light') setScheme(systemScheme());
    };

    webApp?.onEvent?.('themeChanged', onThemeChanged);
    media?.addEventListener?.('change', onMedia);
    return () => {
      webApp?.offEvent?.('themeChanged', onThemeChanged);
      media?.removeEventListener?.('change', onMedia);
    };
  }, []);

  return { scheme, cssVars };
}

/* ─── Корень приложения ─────────────────────────────────────────────────── */

interface AppRootProps {
  children: ReactNode;
  /** Платформа клиента (iOS/остальные): влияет на геометрию, не на логику. */
  platform?: 'base' | 'ios';
  className?: string;
}

/** Корень: красит холст фоновой переменной и держит класс темы. */
export function AppRoot({ children, platform = 'base', className }: AppRootProps) {
  const { scheme, cssVars } = useTelegramTheme();
  const classes = ['dl-root', `dl-theme-${scheme}`, platform === 'ios' ? 'dl-platform-ios' : '', className ?? '']
    .filter(Boolean)
    .join(' ');
  return (
    <div className={classes} style={cssVars}>
      {children}
    </div>
  );
}

/* ─── Секции и ячейки ───────────────────────────────────────────────────── */

/** Контейнер списка: вертикальный ритм между секциями. */
export function List({ children, className, ...rest }: ListProps) {
  return (
    <div className={['dl-list', className ?? ''].filter(Boolean).join(' ')} {...rest}>
      {children}
    </div>
  );
}

interface ListProps extends Record<string, unknown> {
  children: ReactNode;
  className?: string;
  ['data-testid']?: string;
}

/** Секция списка с заголовком/футером (как section у Telegram). */
export function Section({ header, footer, children, className, ...rest }: SectionProps) {
  return (
    <section className={['dl-section', className ?? ''].filter(Boolean).join(' ')} {...rest}>
      {header ? <div className="dl-section__header">{header}</div> : null}
      <div className="cell-list">{children}</div>
      {footer ? <div className="dl-section__footer">{footer}</div> : null}
    </section>
  );
}

interface SectionProps extends Record<string, unknown> {
  header?: string;
  footer?: string;
  children: ReactNode;
  className?: string;
  ['data-testid']?: string;
}

interface CellProps {
  /** Корневой элемент: button/div/label — семантика решает доступность. */
  Component?: 'button' | 'div' | 'label';
  children?: ReactNode;
  before?: ReactNode;
  after?: ReactNode;
  subtitle?: ReactNode;
  multiline?: boolean;
  className?: string;
  onClick?: () => void;
  disabled?: boolean;
  ['aria-expanded']?: boolean;
  ['data-testid']?: string;
  type?: 'button';
}

/**
 * Секционная ячейка (как cell в макете): иконка/аватар слева, заголовок и
 * подпись в центре, правый блок after. Кнопка-ячейка растягивается на всю
 * строку и наследует цвет текста.
 */
export function Cell({
  Component = 'div',
  children,
  before,
  after,
  subtitle,
  multiline,
  className,
  onClick,
  disabled,
  ...rest
}: CellProps) {
  const classes = ['dl-cell', multiline ? 'dl-cell--multiline' : '', className ?? '']
    .filter(Boolean)
    .join(' ');
  return (
    <Component
      className={classes}
      onClick={onClick}
      disabled={Component === 'button' ? disabled : undefined}
      {...rest}
    >
      {before ? <span className="dl-cell__before">{before}</span> : null}
      <span className="dl-cell__main">
        <span className="dl-cell__title">{children}</span>
        {subtitle ? <span className="dl-cell__subtitle">{subtitle}</span> : null}
      </span>
      {after ? <span className="dl-cell__after">{after}</span> : null}
    </Component>
  );
}

/* ─── Пустые состояния и загрузка ───────────────────────────────────────── */

interface PlaceholderProps {
  header: string;
  description?: ReactNode;
  action?: ReactNode;
}

/** Пустое состояние: заголовок, пояснение, действие (как .empty макета). */
export function Placeholder({ header, description, action }: PlaceholderProps) {
  return (
    <div className="dl-empty">
      <div className="dl-empty__title">{header}</div>
      {description ? <p className="dl-empty__text">{description}</p> : null}
      {action ? <div className="dl-empty__action">{action}</div> : null}
    </div>
  );
}

/** Индикатор загрузки (кольцо). */
export function Spinner({ size = 'm' }: { size?: 's' | 'm' | 'l' }) {
  return (
    <span className={`dl-spinner dl-spinner--${size}`} role="status" aria-label="Загрузка">
      <svg viewBox="0 0 24 24" fill="none" aria-hidden>
        <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" opacity=".25" />
        <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      </svg>
    </span>
  );
}

/* ─── Кнопки ────────────────────────────────────────────────────────────── */

interface ButtonProps {
  children: ReactNode;
  size?: 's' | 'm' | 'l';
  mode?: 'filled' | 'gray' | 'plain';
  stretched?: boolean;
  loading?: boolean;
  disabled?: boolean;
  className?: string;
  onClick?: () => void;
  ['data-testid']?: string;
}

/** Кнопка: filled — основная (button_color), gray — нейтральная, plain — текстовая. */
export function Button({
  children,
  size = 'm',
  mode = 'filled',
  stretched,
  loading,
  disabled,
  className,
  onClick,
  ...rest
}: ButtonProps) {
  const classes = [
    'dl-button',
    `dl-button--${size}`,
    `dl-button--${mode}`,
    stretched ? 'dl-button--stretched' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <button
      type="button"
      className={classes}
      disabled={disabled || loading}
      onClick={onClick}
      {...rest}
    >
      {loading ? <Spinner size="s" /> : null}
      {children}
    </button>
  );
}

/* ─── Поля формы ────────────────────────────────────────────────────────── */

interface FieldBase {
  header?: string;
  status?: 'default' | 'error';
  className?: string;
  ['data-testid']?: string;
}

interface InputProps extends FieldBase {
  value: string;
  onChange?: (e: React.ChangeEvent<HTMLInputElement>) => void;
  /** Событие ДО ввода (beforeinput): отличает ручной ввод от программной
   * подстановки значения — onChange не различает их. */
  onBeforeInput?: React.FormEventHandler<HTMLInputElement>;
  onBlur?: () => void;
  placeholder?: string;
  type?: string;
  maxLength?: number;
  disabled?: boolean;
  readOnly?: boolean;
  inputMode?: 'text' | 'numeric' | 'tel' | 'url';
  min?: number;
  'aria-label'?: string;
}

/** Поле ввода: label с подписью и input (как .field/.input макета). */
export function Input({ header, status = 'default', className, placeholder, ...rest }: InputProps) {
  const showDatePlaceholder = rest.type === 'date' && !rest.value && Boolean(placeholder);
  return (
    <label className={['dl-field', status === 'error' ? 'dl-field--error' : '', className ?? '']
      .filter(Boolean)
      .join(' ')}>
      {header ? <span className="dl-field__label">{header}</span> : null}
      <div className="dl-input-wrap">
        <input
          className={['dl-input', showDatePlaceholder ? 'dl-input--empty-date' : ''].filter(Boolean).join(' ')}
          placeholder={placeholder}
          {...rest}
        />
        {showDatePlaceholder ? (
          <span className="dl-input-placeholder" aria-hidden="true">{placeholder}</span>
        ) : null}
      </div>
    </label>
  );
}

interface TextareaProps extends FieldBase {
  value: string;
  onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
  placeholder?: string;
  maxLength?: number;
}

export function Textarea({ header, status = 'default', className, ...rest }: TextareaProps) {
  return (
    <label className={['dl-field', status === 'error' ? 'dl-field--error' : '', className ?? '']
      .filter(Boolean)
      .join(' ')}>
      {header ? <span className="dl-field__label">{header}</span> : null}
      <textarea className="dl-input dl-textarea" {...rest} />
    </label>
  );
}

interface SelectProps extends FieldBase {
  value: string;
  disabled?: boolean;
  onChange?: (e: React.ChangeEvent<HTMLSelectElement>) => void;
  'aria-label'?: string;
  children: ReactNode;
}

export function Select({ header, status = 'default', className, children, ...rest }: SelectProps) {
  return (
    <label className={['dl-field', status === 'error' ? 'dl-field--error' : '', className ?? '']
      .filter(Boolean)
      .join(' ')}>
      {header ? <span className="dl-field__label">{header}</span> : null}
      <select className="dl-input dl-select" {...rest}>
        {children}
      </select>
    </label>
  );
}

interface SwitchProps {
  checked: boolean;
  onChange?: (e: React.ChangeEvent<HTMLInputElement>) => void;
  disabled?: boolean;
  ['data-testid']?: string;
}

/** Переключатель: нативный checkbox (роль и состояние — бесплатно). */
export function Switch({ checked, onChange, disabled, ...rest }: SwitchProps) {
  return <input type="checkbox" className="dl-switch" checked={checked} onChange={onChange} disabled={disabled} {...rest} />;
}

/* ─── Бейджи, разделители, подписи ─────────────────────────────────────── */

interface BadgeProps {
  children: ReactNode;
  mode?: 'primary' | 'secondary' | 'gray';
  ['data-testid']?: string;
  ['data-role']?: string;
}

export function Badge({ children, mode = 'gray', ...rest }: BadgeProps) {
  return (
    <span className={`dl-badge dl-badge--${mode}`} {...rest}>
      {children}
    </span>
  );
}

export function Divider() {
  return <hr className="dl-divider" />;
}

export function Caption({ children, className }: { children: ReactNode; level?: string; className?: string }) {
  return <span className={['dl-caption', className ?? ''].filter(Boolean).join(' ')}>{children}</span>;
}

/* ─── Bottom sheet (Modal) ──────────────────────────────────────────────── */

interface ModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  header?: ReactNode;
  /** Открывается поверх другого шита: чуть выше по z-index. */
  nested?: boolean;
  children: ReactNode;
}

/**
 * Bottom sheet: скрим + лист снизу с грип-полоской. Закрытие — скрим или Esc.
 * Выход анимируется: при open=false 320мс держится «closing»-фаза (лист
 * уезжает вниз, CSS на .dl-modal[data-closing]), затем размонтирование;
 * prefers-reduced-motion убирает фазу.
 */
export function Modal({ open, onOpenChange, header, nested, children }: ModalProps) {
  // Было ли open=true в предыдущем рендере — старт closing-фазы на переходе.
  const [closing, setClosing] = useState(false);
  const wasOpen = useRef(false);

  if (open) {
    wasOpen.current = true;
    if (closing) setClosing(false);
  } else if (wasOpen.current) {
    // Переход true → false: держим контент 320мс для анимации выхода.
    wasOpen.current = false;
    if (!closing) setClosing(true);
  }

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onOpenChange(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onOpenChange]);

  useEffect(() => {
    if (!closing) return;
    // Reduced motion: без анимации выхода — закрываем мгновенно.
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      const id = window.setTimeout(() => setClosing(false), 0);
      return () => window.clearTimeout(id);
    }
    // 320мс — длительность transition листа (dl-modal__sheet в styles.css).
    const id = window.setTimeout(() => setClosing(false), 320);
    return () => window.clearTimeout(id);
  }, [closing]);

  // open=false вне closing-фазы — контент не рендерится.
  if (!open && !closing) return null;

  return (
    <div
      className={nested ? 'dl-modal dl-modal--nested' : 'dl-modal'}
      data-closing={!open ? 'true' : 'false'}
      role="dialog"
      aria-modal="true"
    >
      <div className="dl-modal__scrim" onClick={() => onOpenChange(false)} />
      <div className="dl-modal__sheet">
        <div className="dl-modal__grip" aria-hidden />
        {header}
        <div className="dl-modal__body">{children}</div>
      </div>
    </div>
  );
}
