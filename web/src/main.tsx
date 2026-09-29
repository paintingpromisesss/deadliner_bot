import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import '@telegram-apps/telegram-ui/dist/styles.css';
import './styles.css';

import { initTMA } from './lib/tma';
import { App } from './App';

// SDK инициализируется до первого рендера: launch params нужны и auth-бутстрапу,
// и выбору темы/платформы.
initTMA();

const container = document.getElementById('root');
if (container) {
  createRoot(container).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}