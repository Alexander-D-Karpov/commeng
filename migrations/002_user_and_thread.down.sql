-- Revert only the schema changes introduced by 002.
CREATE INDEX IF NOT EXISTS comments_roots ON comments(post_id, id) WHERE parent_id IS NULL;
CREATE INDEX IF NOT EXISTS comments_replies ON comments(parent_id, id) WHERE parent_id IS NOT NULL;
ALTER TABLE comments DROP COLUMN IF EXISTS thread_blocked;
ALTER TABLE comments DROP COLUMN IF EXISTS is_post_author;
ALTER TABLE comments DROP COLUMN IF EXISTS user_id;
