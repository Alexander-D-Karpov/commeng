CREATE TABLE IF NOT EXISTS users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'USER' CHECK (role IN ('USER','MODERATOR','ADMIN'))
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash text PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_id ON sessions(user_id);

CREATE TABLE IF NOT EXISTS posts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    author_id bigint NOT NULL REFERENCES users(id),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 20000),
    comments_enabled boolean NOT NULL DEFAULT true,
    comment_count bigint NOT NULL DEFAULT 0 CHECK (comment_count >= 0),
    tree_version bigint NOT NULL DEFAULT 1 CHECK (tree_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS comments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
    parent_id bigint,
    author_name text NOT NULL CHECK (char_length(author_name) BETWEEN 1 AND 80),
    author_email text NOT NULL,
    avatar_hash text NOT NULL CHECK (char_length(avatar_hash) = 64),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    state text NOT NULL DEFAULT 'VISIBLE' CHECK (state IN ('VISIBLE','HIDDEN','DELETED')),
    locked boolean NOT NULL DEFAULT false,
    reply_count bigint NOT NULL DEFAULT 0 CHECK (reply_count >= 0),
    likes_count bigint NOT NULL DEFAULT 0 CHECK (likes_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (post_id,id),
    FOREIGN KEY (post_id,parent_id) REFERENCES comments(post_id,id) ON DELETE RESTRICT,
    CHECK (parent_id IS NULL OR parent_id <> id)
);
-- One B-tree serves both oldest/newest traversal and bounded rank scans.
CREATE INDEX IF NOT EXISTS comments_by_parent ON comments(post_id,parent_id,id);
CREATE INDEX IF NOT EXISTS comments_roots ON comments(post_id,id) WHERE parent_id IS NULL;
CREATE INDEX IF NOT EXISTS comments_replies ON comments(parent_id,id) WHERE parent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS comment_likes (
    comment_id bigint NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    actor_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(comment_id,actor_key)
);

CREATE TABLE IF NOT EXISTS moderation_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    comment_id bigint NOT NULL REFERENCES comments(id),
    actor_id bigint NOT NULL REFERENCES users(id),
    action text NOT NULL,
    old_parent_id bigint,
    new_parent_id bigint,
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
