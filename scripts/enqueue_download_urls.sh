#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/enqueue_download_urls.sh <url_list_file> [api_base_url]

Arguments:
  <url_list_file>  1行に1URLを書いたファイル
  [api_base_url]   省略時: http://localhost:8001

Environment variables:
  BATCH_SIZE       1リクエストに送るURL数 (default: 100)
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage >&2
  exit 1
fi

URL_FILE="$1"
API_BASE_URL="${2:-${API_BASE_URL:-http://localhost:8001}}"
ENDPOINT="${API_BASE_URL%/}/api/download"
BATCH_SIZE="${BATCH_SIZE:-100}"

if [[ ! -f "$URL_FILE" ]]; then
  echo "Error: file not found: $URL_FILE" >&2
  exit 1
fi

if ! [[ "$BATCH_SIZE" =~ ^[1-9][0-9]*$ ]]; then
  echo "Error: BATCH_SIZE must be a positive integer (current: $BATCH_SIZE)" >&2
  exit 1
fi

trim() {
  local s="$1"
  s="${s#"${s%%[![:space:]]*}"}"
  s="${s%"${s##*[![:space:]]}"}"
  printf '%s' "$s"
}

json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\t'/\\t}"
  s="${s//$'\r'/}"
  s="${s//$'\n'/}"
  printf '%s' "$s"
}

build_payload() {
  local payload='{"urls":['
  local i
  for ((i = 0; i < ${#BATCH[@]}; i++)); do
    payload+="\"$(json_escape "${BATCH[$i]}")\""
    if ((i < ${#BATCH[@]} - 1)); then
      payload+=","
    fi
  done
  payload+="]}"
  printf '%s' "$payload"
}

send_batch() {
  if ((${#BATCH[@]} == 0)); then
    return 0
  fi

  local payload
  payload="$(build_payload)"

  local response body status
  response="$(curl -sS -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    --data "$payload" \
    -w $'\n%{http_code}')"
  body="${response%$'\n'*}"
  status="${response##*$'\n'}"

  if [[ "$status" =~ ^2[0-9][0-9]$ ]]; then
    sent_count=$((sent_count + ${#BATCH[@]}))
    echo "OK  : sent ${#BATCH[@]} URLs (total sent: $sent_count)"
  else
    failed_batches=$((failed_batches + 1))
    echo "FAIL: endpoint returned HTTP $status" >&2
    echo "Body: $body" >&2
  fi

  BATCH=()
}

declare -a BATCH=()
total_lines=0
effective_urls=0
sent_count=0
failed_batches=0

while IFS= read -r raw_line || [[ -n "$raw_line" ]]; do
  total_lines=$((total_lines + 1))
  line="$(trim "$raw_line")"
  if [[ -z "$line" || "$line" == \#* ]]; then
    continue
  fi

  effective_urls=$((effective_urls + 1))
  BATCH+=("$line")

  if ((${#BATCH[@]} >= BATCH_SIZE)); then
    send_batch
  fi
done < "$URL_FILE"

send_batch

echo "Done."
echo "  endpoint      : $ENDPOINT"
echo "  total lines   : $total_lines"
echo "  effective urls: $effective_urls"
echo "  sent urls     : $sent_count"
echo "  failed batches: $failed_batches"

if ((failed_batches > 0)); then
  exit 1
fi
