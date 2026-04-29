#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT_DIR}"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

FS_CLI_PASSWORD=""
if [[ -f config/freeswitch/autoload_configs/event_socket.conf.xml ]]; then
  FS_CLI_PASSWORD="$(sed -n 's/.*<param name="password" value="\([^"]*\)".*/\1/p' \
    config/freeswitch/autoload_configs/event_socket.conf.xml | head -n 1)"
fi

mask_secret() {
  local value="${1:-}"
  if [[ -z "${value}" ]]; then
    printf "<unset>"
    return
  fi
  if [[ ${#value} -le 8 ]]; then
    printf "%s" "${value}"
    return
  fi
  printf "%s...%s" "${value:0:4}" "${value: -4}"
}

collect_ips() {
  local ips=()

  if command -v ipconfig >/dev/null 2>&1; then
    for iface in en0 en1; do
      local ip
      ip="$(ipconfig getifaddr "${iface}" 2>/dev/null || true)"
      if [[ -n "${ip}" ]]; then
        ips+=("${ip}")
      fi
    done
  fi

  if [[ ${#ips[@]} -eq 0 ]] && command -v hostname >/dev/null 2>&1; then
    local host_ips
    host_ips="$(hostname -I 2>/dev/null || true)"
    if [[ -n "${host_ips}" ]]; then
      # shellcheck disable=SC2206
      ips=(${host_ips})
    fi
  fi

  if [[ ${#ips[@]} -eq 0 ]] && command -v ifconfig >/dev/null 2>&1; then
    while IFS= read -r ip; do
      ips+=("${ip}")
    done < <(ifconfig | awk '/inet / {print $2}' | grep -v '^127\.' | sort -u)
  fi

  printf "%s\n" "${ips[@]}" | awk 'NF && !seen[$0]++'
}

preferred_host_ip() {
  if command -v route >/dev/null 2>&1 && command -v ipconfig >/dev/null 2>&1; then
    local default_if
    default_if="$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')"
    if [[ -n "${default_if}" ]]; then
      ipconfig getifaddr "${default_if}" 2>/dev/null || true
      return
    fi
  fi
}

registered_extensions() {
  if ! command -v docker >/dev/null 2>&1; then
    return
  fi
  if [[ -z "${FS_CLI_PASSWORD}" ]]; then
    return
  fi

  docker exec vbgw-freeswitch \
    fs_cli -H 127.0.0.1 -P 8021 -p "${FS_CLI_PASSWORD}" -x "show registrations" 2>/dev/null \
    | awk -F',' 'NR > 1 && $1 ~ /^[0-9]+$/ {print $1}'
}

recommend_extension() {
  local registered_csv=",$1,"
  local ext
  for ext in $(seq 1000 1019); do
    if [[ "${registered_csv}" != *",${ext},"* ]]; then
      printf "%s" "${ext}"
      return
    fi
  done
  printf "all-used"
}

echo "VBGW Softphone Test Info"
echo "========================"
echo "Repo: ${ROOT_DIR}"
echo

echo "[Service Status]"
if command -v docker >/dev/null 2>&1; then
  docker compose ps || true
else
  echo "docker not found"
fi
echo

echo "[SIP Account]"
echo "Advertised SIP IP (.env): ${EXTERNAL_SIP_IP:-<unset>}"
echo "Advertised RTP IP (.env): ${EXTERNAL_RTP_IP:-<unset>}"
lan_ips="$(collect_ips || true)"
preferred_ip="$(preferred_host_ip || true)"
if [[ -n "${lan_ips}" ]]; then
  if [[ -n "${preferred_ip}" ]]; then
    echo "Host (recommended on macOS Docker): ${preferred_ip}"
  fi
  echo "Host (same LAN):"
  while IFS= read -r ip; do
    [[ -n "${ip}" ]] && echo "  - ${ip}"
  done <<< "${lan_ips}"
fi
echo "Port: 5060"
echo "Protocol: SIP UDP/TCP"
echo "Extension range: 1000-1019"
registered="$(registered_extensions | tr '\n' ' ' | xargs || true)"
recommended="$(recommend_extension "${registered// /,}")"
if [[ -n "${registered}" ]]; then
  echo "Currently registered: ${registered}"
fi
echo "Recommended extension: ${recommended}"
echo "Password (.env ESL_PASSWORD): $(mask_secret "${ESL_PASSWORD:-}")"
echo

echo "[Test Call]"
echo "Dial: 9196"
echo "Purpose: FreeSWITCH -> Bridge -> AI quick test"
echo
echo "[Dev Scenarios]"
echo "9296: AI greeting sanity check"
echo "9297: queue overflow verification"
echo "9298: human fallback verification"
echo "Prime queue/human overflow: ./scripts/prime_dev_overflow.sh queue|human"
echo

echo "[Useful Commands]"
echo "./scripts/sync_external_ip.sh local      # same Mac Zoiper (host IP)"
echo "./scripts/sync_external_ip.sh lan        # another device on reachable LAN"
echo "docker compose logs -f freeswitch"
echo "docker compose logs -f bridge"
echo "docker compose logs -f vbgw-ai"
echo "docker exec vbgw-freeswitch fs_cli -x 'show registrations'"
