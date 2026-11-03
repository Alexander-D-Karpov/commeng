-- Destructive: only run against a deliberately selected database.
DROP TABLE IF EXISTS moderation_audit;
DROP TABLE IF EXISTS comment_likes;
DROP TABLE IF EXISTS comments;
DROP TABLE IF EXISTS posts;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
