#!/usr/bin/env bash
set -euo pipefail
umask 077
: "${DATABASE_URL:?DATABASE_URL must be set}"
: "${BACKUP_DIR:=./.local/backups}"
: "${BACKUP_KEEP_DAYS:=30}"
if ! [[ "$BACKUP_KEEP_DAYS" =~ ^[0-9]+$ ]] || (( BACKUP_KEEP_DAYS < 1 )); then
  echo 'BACKUP_KEEP_DAYS must be a positive number' >&2; exit 2
fi
command -v pg_dump >/dev/null || { echo 'pg_dump is required' >&2; exit 1; }
command -v sha256sum >/dev/null || { echo 'sha256sum is required' >&2; exit 1; }
mkdir -p -- "$BACKUP_DIR"
filename="commeng-$(date -u +%Y%m%dT%H%M%SZ)-$$.dump"
partial="$(mktemp "$BACKUP_DIR/.commeng-backup-XXXXXXXX")"
trap 'rm -f -- "$partial"' EXIT
pg_dump --dbname="$DATABASE_URL" --format=custom --no-owner --no-privileges --file="$partial"
test -s "$partial"
chmod 600 "$partial"
mv -- "$partial" "$BACKUP_DIR/$filename"
trap - EXIT
sha256sum "$BACKUP_DIR/$filename" > "$BACKUP_DIR/$filename.sha256"
echo "Backup created: $BACKUP_DIR/$filename"
# Only prune files matching our own naming convention in this directory.
find "$BACKUP_DIR" -maxdepth 1 -type f -name 'commeng-*.dump*' -mtime "+$BACKUP_KEEP_DAYS" -delete
