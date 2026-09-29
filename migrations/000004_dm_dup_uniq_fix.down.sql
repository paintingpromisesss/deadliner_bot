-- Откат: возвращаем прежний (более строгий) индекс. Если дедлайн успел
-- сделать несколько fan-out, для пары (deadline_id, target_user_id) накопилось
-- несколько dm_dup-строк — их нужно схлопнуть, иначе CREATE UNIQUE INDEX
-- упадёт. Оставляем самую раннюю строку (её fire_at — момент первого fan-out).
DELETE FROM reminders r
 USING reminders keep
 WHERE r.kind = 'dm_dup'
   AND keep.kind = 'dm_dup'
   AND r.deadline_id = keep.deadline_id
   AND r.target_user_id = keep.target_user_id
   AND (r.fire_at, r.id) > (keep.fire_at, keep.id);

DROP INDEX reminders_dm_dup_target_uniq;
CREATE UNIQUE INDEX reminders_dm_dup_target_uniq
    ON reminders (deadline_id, target_user_id)
    WHERE kind = 'dm_dup';