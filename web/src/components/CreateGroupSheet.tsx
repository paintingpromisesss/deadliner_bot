// Шит «Создать группу» (спека §9, экран 5): номер (слаг) + название.
// Слаг нормализуется и проверяется на клиенте ДО запроса — зеркалом правил
// backend (lib/slug.ts ← domain/slug.go). Причина не в удобстве: каждая
// неудачная попытка тратит серверный счётчик «3 группы в сутки» (спека §3.3),
// поэтому явно ошибочные данные не должны доходить до сети. Ответы сервера
// (409 занят, 400 slug_invalid, 429 лимит) показываются здесь же строкой
// role="alert".
import { useEffect, useState } from 'react';
import { Button, Input, Modal } from './ui';
import { SheetHeader } from './SheetHeader';
import { useCreateGroup } from '../lib/queries';
import { checkSlug } from '../lib/slug';
import { groupCreateErrorMessage, groupTitleErrorMessage, slugErrorMessage } from '../lib/errorText';
import { strings } from '../lib/strings';
import { hapticImpact } from '../lib/tma';

interface CreateGroupSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Успех: вызывающий закрывает шит и может перейти в созданную группу. */
  onCreated?: (groupID: number) => void;
}

export function CreateGroupSheet({ open, onOpenChange, onCreated }: CreateGroupSheetProps) {
  const create = useCreateGroup();
  const [slug, setSlug] = useState('');
  const [title, setTitle] = useState('');
  const [attempted, setAttempted] = useState(false);
  const [apiError, setApiError] = useState<string | null>(null);

  // Открытие шита — чистое состояние: иначе после закрытия с ошибкой «номер
  // занят» следующий заход начинался бы с чужого текста и заполненных полей.
  useEffect(() => {
    if (!open) return;
    setSlug('');
    setTitle('');
    setAttempted(false);
    setApiError(null);
  }, [open]);

  const slugCheck = checkSlug(slug);
  const titleError = title.trim() === '' ? 'title_required' : null;
  const slugError = slugCheck.error;
  const valid = slugError === null && titleError === null;

  async function submit() {
    setAttempted(true);
    setApiError(null);
    if (!valid) {
      hapticImpact('rigid');
      return;
    }
    try {
      const { group } = await create.mutateAsync({ slug: slugCheck.normalized, title: title.trim() });
      onOpenChange(false);
      onCreated?.(group.id);
    } catch (e) {
      setApiError(groupCreateErrorMessage(e));
    }
  }

  const showSlugError = attempted && slugError !== null;

  return (
    <Modal open={open} onOpenChange={onOpenChange} header={<SheetHeader title={strings.groups.createTitle} onClose={() => onOpenChange(false)} />}>
      <div className="dl-sheet" data-testid="create-group-sheet">
        <Input
          header={strings.groups.fieldSlug}
          placeholder={strings.groups.fieldSlugPlaceholder}
          value={slug}
          maxLength={24}
          status={showSlugError ? 'error' : 'default'}
          onChange={(e) => setSlug(e.target.value)}
          onBlur={() => setAttempted(true)}
          data-testid="field-slug"
        />
        <div className="dl-hint">{strings.groups.slugHint}</div>
        {showSlugError && slugError ? (
          <div className="dl-error" role="alert" data-testid="error-slug">
            {slugErrorMessage(slugError)}
          </div>
        ) : null}

        <Input
          header={strings.groups.fieldTitle}
          placeholder={strings.groups.fieldTitlePlaceholder}
          value={title}
          maxLength={200}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={() => setAttempted(true)}
          data-testid="field-group-title"
        />
        {attempted && titleError ? (
          <div className="dl-error" role="alert" data-testid="error-group-title">
            {groupTitleErrorMessage(titleError)}
          </div>
        ) : null}

        {apiError ? (
          <div className="dl-error" role="alert" data-testid="create-error">
            {apiError}
          </div>
        ) : null}

        <div className="dl-hint">{strings.groups.createHint}</div>

        <div className="dl-row">
          <Button
            size="l"
            stretched
            loading={create.isPending}
            disabled={create.isPending}
            data-testid="submit-create-group"
            onClick={() => void submit()}
          >
            {strings.groups.createSubmit}
          </Button>
        </div>
      </div>
    </Modal>
  );
}