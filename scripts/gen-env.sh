#!/usr/bin/env bash
# Generate a project-root .env and ./data/admin-credentials.txt once for first install.
# Never overwrites existing files. Safe to re-run.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

ENV_FILE="${ENV_FILE:-$ROOT/.env}"
DATA_DIR="${DATA_DIR:-$ROOT/data}"
CREDENTIALS_FILE="${CREDENTIALS_FILE:-$DATA_DIR/admin-credentials.txt}"
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

mkdir -p "$DATA_DIR"

need_env=0
need_cred=0
[[ -e "$ENV_FILE" ]] || need_env=1
[[ -e "$CREDENTIALS_FILE" ]] || need_cred=1

if [[ "$need_env" -eq 0 && "$need_cred" -eq 0 ]]; then
  echo "Both already exist — nothing written:"
  echo "  $ENV_FILE"
  echo "  $CREDENTIALS_FILE"
  echo "Filehub never overwrites these. Remove them only if you intentionally want a new first-boot seed (and usually a fresh DB)."
  exit 0
fi

# Reuse values from whichever file already exists
existing_pw=""
existing_secret=""
existing_user=""
for f in "$ENV_FILE" "$CREDENTIALS_FILE"; do
  if [[ -f "$f" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      line="${line%$'\r'}"
      [[ "$line" =~ ^# ]] && continue
      [[ -z "$line" ]] && continue
      key="${line%%=*}"
      val="${line#*=}"
      case "$key" in
        ADMIN_USER) existing_user="$val" ;;
        ADMIN_PASSWORD) existing_pw="$val" ;;
        SESSION_SECRET) existing_secret="$val" ;;
      esac
    done <"$f"
  fi
done

if [[ -n "$existing_user" ]]; then
  ADMIN_USER="$existing_user"
fi
if ! is_placeholder "$existing_pw"; then
  ADMIN_PASSWORD="$existing_pw"
else
  ADMIN_PASSWORD="$(rand_alnum 24)"
fi
if ! is_placeholder "$existing_secret"; then
  SESSION_SECRET="$existing_secret"
else
  SESSION_SECRET="$(rand_alnum 48)"
fi

ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
body=$(cat <<BODY
# Filehub first-boot credentials — generated once. Do not commit.
# Generated (UTC): ${ts}
# Read this file, sign in, then change the password under Settings → Change password.
# Later runs of scripts/gen-env.sh and the Docker entrypoint will NOT overwrite these files.
ADMIN_USER=${ADMIN_USER}
ADMIN_PASSWORD=${ADMIN_PASSWORD}
SESSION_SECRET=${SESSION_SECRET}
BODY
)

wrote=()
if [[ "$need_env" -eq 1 ]]; then
  # Prefer copying from .env.example structure when present, but always set secrets.
  if [[ -f "$ROOT/.env.example" ]]; then
    # Start from example, then overwrite the three keys
    cp "$ROOT/.env.example" "$ENV_FILE"
    # Rewrite the three keys robustly
    tmp="${ENV_FILE}.tmp.$$"
    awk -v u="$ADMIN_USER" -v p="$ADMIN_PASSWORD" -v s="$SESSION_SECRET" '
      BEGIN{done_u=0;done_p=0;done_s=0}
      /^ADMIN_USER=/{print "ADMIN_USER=" u; done_u=1; next}
      /^ADMIN_PASSWORD=/{print "ADMIN_PASSWORD=" p; done_p=1; next}
      /^SESSION_SECRET=/{print "SESSION_SECRET=" s; done_s=1; next}
      {print}
      END{
        if(!done_u) print "ADMIN_USER=" u
        if(!done_p) print "ADMIN_PASSWORD=" p
        if(!done_s) print "SESSION_SECRET=" s
      }
    ' "$ENV_FILE" >"$tmp"
    mv "$tmp" "$ENV_FILE"
  else
    printf '%s\n' "$body" >"$ENV_FILE"
  fi
  chmod 600 "$ENV_FILE" || true
  wrote+=("$ENV_FILE")
else
  echo "Keeping existing $ENV_FILE (not overwritten)"
fi

if [[ "$need_cred" -eq 1 ]]; then
  printf '%s\n' "$body" >"$CREDENTIALS_FILE"
  chmod 600 "$CREDENTIALS_FILE" || true
  wrote+=("$CREDENTIALS_FILE")
else
  echo "Keeping existing $CREDENTIALS_FILE (not overwritten)"
fi

echo "============================================================"
echo "Filehub install: credentials generated (once)."
echo "  Username: ${ADMIN_USER}"
echo "  Password file: ${CREDENTIALS_FILE}"
if [[ "$need_env" -eq 1 ]]; then
  echo "  Compose env:   ${ENV_FILE}"
fi
echo "  Sign in, then change password under Settings → Change password."
echo "============================================================"
if [[ ${#wrote[@]} -gt 0 ]]; then
  echo "Wrote:"
  for w in "${wrote[@]}"; do
    echo "  $w"
  done
fi
