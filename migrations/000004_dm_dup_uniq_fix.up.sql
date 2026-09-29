-- Фикс уникальности dm_dup-детей (спека §7.3).
--
-- Прежний unique-индекс (deadline_id, target_user_id) WHERE kind='dm_dup'
-- запрещал более одного дубля в ЛС на участника для ВСЕГО дедлайна: у
-- дедлайна с несколькими пресетами (24ч + 1ч) второй fan-out не создавал
-- детей вовсе, и участник получал одно ЛС вместо N. Барьер от дублей должен
-- работать в пределах ОДНОГО fan-out, а не дедлайна.
--
-- Новый индекс добавляет fire_at (момент fan-out): дети одного fan-out
-- пишутся с одинаковым fire_at = now → гонка/повтор по-прежнему схлопывается
-- ON CONFLICT DO NOTHING; дети разных fan-out различаются по fire_at →
-- каждое напоминание доставляет свои дубли.
DROP INDEX reminders_dm_dup_target_uniq;
CREATE UNIQUE INDEX reminders_dm_dup_target_uniq
    ON reminders (deadline_id, target_user_id, fire_at)
    WHERE kind = 'dm_dup';