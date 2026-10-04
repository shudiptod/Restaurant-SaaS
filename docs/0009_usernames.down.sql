DROP INDEX IF EXISTS users_username_unique;
ALTER TABLE users DROP COLUMN IF EXISTS username;