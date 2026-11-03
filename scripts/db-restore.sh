#!/usr/bin/env bash
set -euo pipefail
umask 077
: "${DATABASE_URL:?DATABASE_URL must be set}"
backup=${1:-}
if [[ -z "$backup" || ! -f "$backup" ]]; then
  echo 'Usage: DATABASE_URL=... CONFIRM_RESTORE=YES scripts/db-restore.sh /path/to/backup.dump' >&2
  exit 2
fi
if [[ "${CONFIRM_RESTORE:-}" != "YES" ]]; then
  echo 'Restore is DESTRUCTIVE; set CONFIRM_RESTORE=YES after creating a backup.' >&2
  exit 2
fi
command -v pg_restore >/dev/null || { echo 'pg_restore is required' >&2; exit 1; }
if [[ -f "$backup.sha256" ]]; then
  (cd -- "$(dirname -- "$backup")" && sha256sum --check "$(basename -- "$backup.sha256")")
else
  echo 'Warning: no SHA256 checksum accompanies this archive' >&2
fi
echo 'STOP application writers and ensure target connection string is correct BEFORE restoring.' >&2
# Keep this a separate explicit command: no automatic production restores.
pg_restore --dbname="$DATABASE_URL" --clean --if-exists --no-owner --no-privileges --exit-on-error "$backup"
echo 'Restore completed; run commeng migrate status and application smoke tests.'
