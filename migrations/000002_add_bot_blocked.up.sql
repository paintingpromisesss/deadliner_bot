-- users.bot_blocked: set when the bot gets 403 (user blocked) on a DM send,
-- so dm_dup fan-out (spec §7.3) skips the user. Pulled from Task 9 into Task 4
-- by controller ruling: domain.User.BotBlocked and UserRepo.MarkBotBlocked
-- already exist and the repo tests need the column.
ALTER TABLE users ADD COLUMN bot_blocked boolean NOT NULL DEFAULT false;
