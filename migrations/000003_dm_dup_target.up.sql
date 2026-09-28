-- reminders.target_user_id: цель dm_dup fan-out (спека §7.3). NULL для
-- обычных reminder-строк (preset/custom_offset/custom_at).
ALTER TABLE reminders ADD COLUMN target_user_id bigint;

-- dm_dup-дети одного родителя имеют одинаковые (deadline_id, fire_at, kind),
-- но разные target_user_id: прежний полный unique-индекс не дал бы создать
-- более одного ребёнка. Ограничиваем индекс строками без цели.
DROP INDEX reminders_deadline_fire_kind_uniq;
CREATE UNIQUE INDEX reminders_deadline_fire_kind_uniq
    ON reminders (deadline_id, fire_at, kind)
    WHERE target_user_id IS NULL;

-- Поиск dm_dup-строк по цели (воркер: SendToUser(target)).
CREATE INDEX reminders_dm_dup_target_idx
    ON reminders (kind, target_user_id)
    WHERE kind = 'dm_dup';

-- Страховка от дублей детей: один ребёнок на (дедлайн, цель). Даёт смысл
-- ON CONFLICT DO NOTHING в MarkSentWithFanout при гонках/повторах.
CREATE UNIQUE INDEX reminders_dm_dup_target_uniq
    ON reminders (deadline_id, target_user_id)
    WHERE kind = 'dm_dup';
