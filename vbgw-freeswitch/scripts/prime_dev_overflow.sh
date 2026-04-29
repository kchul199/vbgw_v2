#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT_DIR}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found"
  exit 1
fi

if [[ ! -f config/freeswitch/autoload_configs/event_socket.conf.xml ]]; then
  echo "FreeSWITCH ESL config not found"
  exit 1
fi

FS_CLI_PASSWORD="$(sed -n 's/.*<param name="password" value="\([^"]*\)".*/\1/p' \
  config/freeswitch/autoload_configs/event_socket.conf.xml | head -n 1)"

if [[ -z "${FS_CLI_PASSWORD}" ]]; then
  echo "Could not determine FreeSWITCH ESL password"
  exit 1
fi

usage() {
  cat <<'EOF'
Usage:
  ./scripts/prime_dev_overflow.sh queue
  ./scripts/prime_dev_overflow.sh human

Scenarios:
  queue  Prime 9297 so the next Zoiper call goes into queue overflow.
  human  Prime 9298 so the next Zoiper call goes into human fallback.
EOF
}

scenario="${1:-}"

case "${scenario}" in
  queue)
    target="9297"
    blocker_cli="9907"
    blocker_name="dev-hold-queue"
    summary="Next Zoiper call to 9297 should hear the queue cadence and then timeout."
    ;;
  human)
    target="9298"
    blocker_cli="9908"
    blocker_name="dev-hold-human"
    summary="Next Zoiper call to 9298 should overflow to the human queue. Keep extension 1001 registered on the agent softphone."
    ;;
  *)
    usage
    exit 1
    ;;
esac

fs_cli() {
  docker exec vbgw-freeswitch \
    fs_cli -H 127.0.0.1 -P 8021 -p "${FS_CLI_PASSWORD}" -x "$1"
}

echo "[1/3] Clearing previous blocker ${blocker_cli}..."
fs_cli "hupall NORMAL_CLEARING origination_caller_id_number ${blocker_cli}" >/dev/null 2>&1 || true

echo "[2/3] Priming overflow path for ${target}..."
fs_cli "originate {origination_caller_id_name=${blocker_name},origination_caller_id_number=${blocker_cli},ignore_early_media=true}loopback/${target}/default &park()" >/dev/null

echo "[3/3] Current registrations"
fs_cli "show registrations" || true

if [[ "${scenario}" == "human" ]]; then
  echo
  echo "[Callcenter Agents]"
  fs_cli "callcenter_config agent list" || true
fi

echo
echo "${summary}"
