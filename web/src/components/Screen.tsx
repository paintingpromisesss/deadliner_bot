// Каркас экрана: крупный заголовок в стиле Wallet + отступ под нижний таб-бар.
import type { ReactNode } from 'react';

interface ScreenProps {
  title?: string;
  children: ReactNode;
}

export function Screen({ title, children }: ScreenProps) {
  return (
    <div className="dl-screen pt-2">
      {title ? <h1 className="dl-screen__title">{title}</h1> : null}
      {children}
    </div>
  );
}