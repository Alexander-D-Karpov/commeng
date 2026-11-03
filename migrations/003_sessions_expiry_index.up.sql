-- Login cleanup deletes expired sessions; index expires_at for bounded scans.
CREATE INDEX IF NOT EXISTS sessions_expires_at ON sessions(expires_at);
