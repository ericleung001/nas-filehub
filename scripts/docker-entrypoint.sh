#!/bin/sh
# Filehub Docker entrypoint: on first boot, auto-generate ADMIN_PASSWORD +
# SESSION_SECRET into /data/.env and a one-time credentials file. Never
# overwrites an existing .env or credentials file.
set -eu

CREDENTIALS_FILE="${CREDENTIALS_FILE:-/data/admin-credentials.txt}"
DOTENV_FILE="${DOTENV_FILE:-/data/.env}"
ADMIN_USER="${ADMIN_USER:-admin}"

is_placeholder() {
  case "${1:-}" in
    ""|changeme|changeme-please|replace-me|replace-with-long-random|replace-with-long-random-string)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

rand_alnum() {
  len="$1"
  # pipefail-safe: consume a fixed block, then trim (avoid SIGPIPE from head closing early)
  dd if=/dev/urandom bs=64 count=1 2>/dev/null | base64 | tr -d '\n/+=' | cut -c1-"$len"
}

read_kv() {
  file="$1"
  key="$2"
  if [ ! -f "$file" ]; then
    return 0
  fi
  line=$(grep -E "^${key}=" "$file" 2>/dev/null | head -n 1 | tr -d '\r' || true)
  if [ -z "$line" ]; then
    return 0
  fi
  printf '%s' "${line#*=}"
}

load_from_file() {
  file="$1"
  [ -f "$file" ] || return 0
  u=$(read_kv "$file" ADMIN_USER)
  p=$(read_kv "$file" ADMIN_PASSWORD)
  s=$(read_kv "$file" SESSION_SECRET)
  if [ -n "$u" ]; then
    ADMIN_USER="$u"
  fi
  if is_placeholder "${ADMIN_PASSWORD:-}" && [ -n "$p" ] && ! is_placeholder "$p"; then
    ADMIN_PASSWORD="$p"
  fi
  if is_placeholder "${SESSION_SECRET:-}" && [ -n "$s" ] && ! is_placeholder "$s"; then
    SESSION_SECRET="$s"
  fi
}

write_file_once() {
  path="$1"
  body="$2"
  if [ -e "$path" ]; then
    echo "filehub: ${path} already exists — not overwriting" >&2
    return 1
  fi
  dir=$(dirname "$path")
  mkdir -p "$dir" 2>/dev/null || true
  printf '%s\n' "$body" >"$path"
  chmod 600 "$path" 2>/dev/null || true
  echo "filehub: wrote ${path}" >&2
  return 0
}

mkdir -p "$(dirname "$CREDENTIALS_FILE")" "$(dirname "$DOTENV_FILE")" 2>/dev/null || true

# Prefer values from existing mounted files (never overwritten below)
load_from_file "$CREDENTIALS_FILE"
load_from_file "$DOTENV_FILE"

first_boot_files=0
if [ ! -e "$CREDENTIALS_FILE" ] && [ ! -e "$DOTENV_FILE" ]; then
  first_boot_files=1
fi

if [ "$first_boot_files" -eq 1 ]; then
  # Brand-new install: create secrets if the operator did not supply real ones
  if is_placeholder "${ADMIN_PASSWORD:-}"; then
    ADMIN_PASSWORD=$(rand_alnum 24)
  fi
  if is_placeholder "${SESSION_SECRET:-}"; then
    SESSION_SECRET=$(rand_alnum 48)
  fi
  body=$(printf '%s\n' \
    "# Filehub first-boot credentials — generated once. Do not commit." \
    "# Generated (UTC): $(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)" \
    "# Read this file, sign in, then change the password under Settings → Change password." \
    "# Later starts will NOT overwrite this file." \
    "ADMIN_USER=${ADMIN_USER}" \
    "ADMIN_PASSWORD=${ADMIN_PASSWORD}" \
    "SESSION_SECRET=${SESSION_SECRET}")
  write_file_once "$DOTENV_FILE" "$body" || true
  write_file_once "$CREDENTIALS_FILE" "$body" || true
  echo "============================================================" >&2
  echo "Filehub first boot: admin credentials written (once)." >&2
  echo "  Username: ${ADMIN_USER}" >&2
  echo "  Read the password from (host: ./data/... when compose mounts ./data:/data):" >&2
  echo "    ${CREDENTIALS_FILE}" >&2
  echo "  Env copy:" >&2
  echo "    ${DOTENV_FILE}" >&2
  echo "  Change the password after first login (Settings → Change password)." >&2
  echo "============================================================" >&2
elif is_placeholder "${ADMIN_PASSWORD:-}"; then
  echo "filehub: WARN: ADMIN_PASSWORD unset/placeholder; existing credentials/.env did not supply one." >&2
  echo "filehub: WARN: Refusing to invent a new password (would not match a seeded admin). Set ADMIN_PASSWORD or fix ${CREDENTIALS_FILE}" >&2
fi

# If only one of the two files is missing but we already have real secrets, fill the gap once
if ! is_placeholder "${ADMIN_PASSWORD:-}"; then
  if is_placeholder "${SESSION_SECRET:-}"; then
    SESSION_SECRET=$(rand_alnum 48)
  fi
  body=$(printf '%s\n' \
    "# Filehub first-boot credentials — generated once. Do not commit." \
    "# Generated (UTC): $(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)" \
    "# Read this file, sign in, then change the password under Settings → Change password." \
    "# Later starts will NOT overwrite this file." \
    "ADMIN_USER=${ADMIN_USER}" \
    "ADMIN_PASSWORD=${ADMIN_PASSWORD}" \
    "SESSION_SECRET=${SESSION_SECRET}")
  if [ ! -e "$DOTENV_FILE" ]; then
    write_file_once "$DOTENV_FILE" "$body" || true
  fi
  if [ ! -e "$CREDENTIALS_FILE" ]; then
    write_file_once "$CREDENTIALS_FILE" "$body" || true
  fi
fi

export ADMIN_USER
export ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
export SESSION_SECRET="${SESSION_SECRET:-}"

exec /app/nas-filehub "$@"
