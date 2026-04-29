#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT_DIR}"

SERVICE_NUMBER="${1:-9390}"
CALLER_ID="${2:-9910}"

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

fs_api() {
  docker exec vbgw-freeswitch sh -lc \
    "/usr/local/freeswitch/bin/fs_cli -p '${FS_CLI_PASSWORD}' -x \"$1\""
}

echo "[1/3] Current registrations"
fs_api "show registrations" || true
echo

echo "[2/3] Originating synthetic call into service ${SERVICE_NUMBER}"
fs_api "originate {origination_caller_id_name=ext-pool-test,origination_caller_id_number=${CALLER_ID},ignore_early_media=true}loopback/${SERVICE_NUMBER}/default &park()"
echo

echo "[3/3] If a SIP extension slot is registered, it should ring now."
echo "Watch logs with: docker compose logs -f orchestrator freeswitch"
