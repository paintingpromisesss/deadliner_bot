// Каркас экрана: крупный заголовок в стиле Wallet + отступ под нижний таб-бар.
import type { ReactNode } from 'react';

interface ScreenProps {
  title?: string;
  onBack?: () => void;
  backLabel?: string;
  children: ReactNode;
}

export function Screen({ title, onBack, backLabel, children }: ScreenProps) {
  return (
    <div className="dl-screen pt-2">
      {onBack ? (
        <div className="dl-screen-back-wrap">
          <button
            type="button"
            className="dl-screen-back"
            onClick={onBack}
            aria-label={backLabel ?? 'Назад'}
            data-testid="screen-back"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <polyline points="15 18 9 12 15 6" />
            </svg>
            <span>{backLabel ?? 'Назад'}</span>
          </button>
        </div>
      ) : null}
      {title ? <h1 className="dl-screen__title">{title}</h1> : null}
      {children}
    </div>
  );
}