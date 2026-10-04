ALTER TABLE users ADD COLUMN username TEXT;
UPDATE users SET username = email WHERE username IS NULL;
ALTER TABLE users ALTER COLUMN username SET NOT NULL;
CREATE UNIQUE INDEX users_username_unique ON users (username);