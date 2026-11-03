-- Keep 001 immutable so existing databases can upgrade in place.
ALTER TABLE comments ADD COLUMN IF NOT EXISTS user_id bigint REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS is_post_author boolean NOT NULL DEFAULT false;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS thread_blocked boolean NOT NULL DEFAULT false;

-- One multicolumn B-tree supports both root and child pages, either direction.
DROP INDEX IF EXISTS comments_roots;
DROP INDEX IF EXISTS comments_replies;

-- Existing comments may predate materialized inherited visibility.
WITH RECURSIVE tree AS (
    SELECT id, false AS blocked,
      (locked OR state <> 'VISIBLE') AS blocks_children
    FROM comments WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, p.blocks_children,
      (p.blocks_children OR c.locked OR c.state <> 'VISIBLE')
    FROM comments c JOIN tree p ON c.parent_id=p.id
)
UPDATE comments c SET thread_blocked=tree.blocked
FROM tree WHERE c.id=tree.id AND c.thread_blocked IS DISTINCT FROM tree.blocked;
