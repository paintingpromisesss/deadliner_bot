-- dm_dup-дети мешают откату: у них совпадают (deadline_id, kind, fire_at),
-- поэтому пересоздание полного unique-индекса ниже упало бы на дубликатах.
-- Дубли в ЛС — производная доставка, при откате схемы их можно удалить.
DELETE FROM reminders WHERE kind = 'dm_dup';

DROP INDEX reminders_dm_dup_target_uniq;
DROP INDEX reminders_dm_dup_target_idx;
DROP INDEX reminders_deadline_fire_kind_uniq;
CREATE UNIQUE INDEX reminders_deadline_fire_kind_uniq
    ON reminders (deadline_id, fire_at, kind);
ALTER TABLE reminders DROP COLUMN target_user_id;