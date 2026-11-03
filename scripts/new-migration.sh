#!/bin/sh
# Generate the next immutable, numbered PostgreSQL migration pair.
set -eu
name=${1:-}
case "$name" in
  ''|*[!a-z0-9_]*|[!a-z]*|_* )
    echo 'Usage: make migrate-new NAME=add_feature (lowercase letters/digits/underscores)' >&2
    exit 2 ;;
esac

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
folder="$root/migrations"
version=$(find "$folder" -maxdepth 1 -name '[0-9][0-9][0-9]_*.up.sql' -print |
  sed -n 's/.*\/\([0-9][0-9][0-9]\)_.*/\1/p' | sort -n | tail -n 1)
version=${version:-000}
next=$(awk -v version="$version" 'BEGIN { printf "%03d", version+1 }')
if [ "$next" -gt 999 ]; then
  echo 'Too many migrations for the 3-digit filename convention' >&2
  exit 1
fi
up="$folder/${next}_${name}.up.sql"
down="$folder/${next}_${name}.down.sql"
if [ -e "$up" ] || [ -e "$down" ]; then
  echo 'Migration already exists' >&2
  exit 1
fi
printf '%s\n' '-- Write the forward SQL here.' > "$up"
printf '%s\n' '-- Write the inverse SQL here; review for data loss.' > "$down"
printf 'Created %s and %s\n' "$up" "$down"
