-- Reverse dependency order.
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS outbox_messages;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_action_counters;
DROP TABLE IF EXISTS claim_codes;
DROP TABLE IF EXISTS invites;
DROP TABLE IF EXISTS reminders;
DROP TABLE IF EXISTS deadlines;
DROP TABLE IF EXISTS group_memberships;
DROP TABLE IF EXISTS chat_bindings;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS users;
