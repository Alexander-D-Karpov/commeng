-- 004: additive online-compatible schema change for edits, durable comment events and notifications.
ALTER TABLE posts ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE posts ADD COLUMN deleted boolean NOT NULL DEFAULT false;
ALTER TABLE posts ADD COLUMN event_version bigint NOT NULL DEFAULT 0 CHECK(event_version>=0);
ALTER TABLE comments ADD COLUMN edited_at timestamptz;

CREATE TABLE comment_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
    sequence bigint NOT NULL CHECK(sequence>0),
    comment_id bigint NOT NULL UNIQUE REFERENCES comments(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(post_id,sequence)
);
CREATE INDEX comment_events_by_post ON comment_events (post_id,sequence);
CREATE INDEX comment_events_created_at ON comment_events(created_at,id);

CREATE TABLE notifications (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    comment_id bigint NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    actor_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
    kind text NOT NULL CHECK (kind IN ('REPLY')),
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at timestamptz,
    UNIQUE (user_id,comment_id,kind)
);
CREATE INDEX notifications_for_user ON notifications(user_id,id DESC);
CREATE INDEX notifications_unread_for_user ON notifications(user_id,id DESC) WHERE read_at IS NULL;

-- Simple config works for both Russian and English terms without language detection.
-- In-memory search uses a documented equivalent substring/term fallback.
CREATE INDEX posts_search_gin ON posts USING gin (
    to_tsvector('simple',coalesce(title,'') || ' ' || coalesce(body,''))
) WHERE deleted=false;
CREATE INDEX comments_search_gin ON comments USING gin (
    to_tsvector('simple',coalesce(body,''))
) WHERE state='VISIBLE' AND thread_blocked=false;

CREATE FUNCTION commeng_notify_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('commeng_events',NEW.id::text);
    RETURN NEW;
END;
$$;
CREATE TRIGGER commeng_outbox_after_insert AFTER INSERT ON comment_events
FOR EACH ROW EXECUTE FUNCTION commeng_notify_event();
