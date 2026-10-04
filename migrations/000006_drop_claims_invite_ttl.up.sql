-- TZ v2: claim-коды полностью удаляются (создатель группы получает роль
-- admin автоматически), инвайты получают опциональный срок жизни
-- (NULL = бессрочный, пока не отозван или не исчерпан лимит).
DROP INDEX IF EXISTS claim_codes_group_unused_idx;
DROP TABLE IF EXISTS claim_codes;

ALTER TABLE invites ALTER COLUMN expires_at DROP NOT NULL;

-- Активация группы теперь не claim'ом, а привязкой чата/создателем:
-- чистим claim_expires_at у существующих групп.
UPDATE groups SET claim_expires_at = NULL WHERE claim_expires_at IS NOT NULL;
