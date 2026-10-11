#!/bin/sh
# Dumps the comment service's database into an archive encrypted with age,
# copies it off the server when BACKUP_REMOTE is set, then prunes archives
# older than BACKUP_KEEP_DAYS on both sides (docs/design/architecture.md
# section 9). Sessions, login transactions, notifications and the mail
# queue keep their tables but not their rows (data-model.md section 1).
#
#   scripts/backup.sh        # in the checkout, e.g. /opt/comments
#
# Needs: docker, age, and rclone for BACKUP_REMOTE. Settings come from the
# environment or deploy/.env (COMMENTS_ENV_FILE names another file):
# BACKUP_AGE_RECIPIENT (age public key), BACKUP_DIR, optional BACKUP_REMOTE
# (an rclone remote:path), BACKUP_KEEP_DAYS (default 30) and
# BACKUP_REMOTE_KEEP_DAYS (default BACKUP_KEEP_DAYS; 0 never deletes there).
# The keys in .env are not in the archive; keep them offline.
set -eu
cd "$(dirname "$0")/.."

env_file=${COMMENTS_ENV_FILE:-deploy/.env}
[ -f "$env_file" ] || {
    echo "missing $env_file" >&2
    exit 2
}
env_file=$(cd "$(dirname "$env_file")" && pwd)/$(basename "$env_file")

# Reads one setting without sourcing the file: compose's .env syntax allows
# values a shell would run as commands, such as COMMENTS_MAIL_FROM.
setting() {
    sed -n "s/^$1=//p" "$env_file" | tail -n 1 | sed "s/^[\"']\(.*\)[\"']\$/\1/"
}

: "${BACKUP_AGE_RECIPIENT:=$(setting BACKUP_AGE_RECIPIENT)}"
: "${BACKUP_DIR:=$(setting BACKUP_DIR)}"
: "${BACKUP_REMOTE:=$(setting BACKUP_REMOTE)}"
: "${BACKUP_KEEP_DAYS:=$(setting BACKUP_KEEP_DAYS)}"
: "${BACKUP_REMOTE_KEEP_DAYS:=$(setting BACKUP_REMOTE_KEEP_DAYS)}"
: "${BACKUP_AGE_RECIPIENT:?set BACKUP_AGE_RECIPIENT (an age public key)}"
: "${BACKUP_DIR:?set BACKUP_DIR}"
keep=${BACKUP_KEEP_DAYS:-30}
remote_keep=${BACKUP_REMOTE_KEEP_DAYS:-$keep}
project=$(setting COMPOSE_PROJECT_NAME)
project=${project:-comments}

# Run where an operator would, so compose reads COMPOSE_FILE and
# COMPOSE_PROJECT_NAME (edge mode, staging) from the env file itself.
compose() {
    (cd deploy && docker compose --env-file "$env_file" "$@")
}

ts=$(date -u +%Y%m%dT%H%M%SZ)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

compose exec -T postgres pg_dump -U comments -d comments -Fc --no-owner \
    --exclude-table-data=console_sessions --exclude-table-data=site_sessions \
    --exclude-table-data=login_transactions --exclude-table-data=notifications \
    --exclude-table-data=outbox >"$work/comments.dump"
printf 'comments_version=%s\ncreated=%s\nproject=%s\n' \
    "$(setting COMMENTS_VERSION)" "$ts" "$project" >"$work/MANIFEST"

mkdir -p "$BACKUP_DIR"
out="$BACKUP_DIR/$project-$ts.tar.age"
tar -C "$work" -cf - MANIFEST comments.dump | age -r "$BACKUP_AGE_RECIPIENT" -o "$out.tmp"
mv "$out.tmp" "$out"
chmod 600 "$out"
echo "backup written: $out ($(wc -c <"$out" | tr -d ' ') bytes)"

# A failed copy stops here, before anything is pruned.
if [ -n "$BACKUP_REMOTE" ]; then
    rclone copy "$out" "$BACKUP_REMOTE"
    echo "copied off-site: $BACKUP_REMOTE"
fi

# "-2" so that comments-* does not also match comments-staging-*.
find "$BACKUP_DIR" -name "$project-2*.tar.age" -mtime +"$keep" -print -delete

if [ -n "$BACKUP_REMOTE" ] && [ "$remote_keep" != 0 ]; then
    rclone delete "$BACKUP_REMOTE" --min-age "${remote_keep}d" --include "$project-2*.tar.age"
fi
