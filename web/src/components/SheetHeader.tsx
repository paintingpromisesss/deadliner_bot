// Заголовок bottom sheet: текст + кнопка закрытия. Modal.Header кита рендерит
// текст только на iOS (на 'base' остаётся пустой блок), поэтому заголовок
// собираем сами. Отдельный компонент — он нужен каждому шиту (дедлайн, группа,
// инвайт, claim), и вторая копия разметки разошлась бы с первой.
import { strings } from '../lib/strings';

interface SheetHeaderProps {
  title: string;
  onClose: () => void;
}

export function SheetHeader({ title, onClose }: SheetHeaderProps) {
  return (
    <div className="dl-sheet-header">
      <span className="dl-sheet-header__title">{title}</span>
      <button
        type="button"
        className="dl-sheet-header__close"
        aria-label={strings.common.close}
        onClick={onClose}
      >
        ✕
      </button>
    </div>
  );
}