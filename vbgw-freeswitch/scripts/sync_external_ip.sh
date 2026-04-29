#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/.env"
MODE="${1:-lan}"

if [ ! -f "${ENV_FILE}" ]; then
  echo ".env file not found: ${ENV_FILE}" >&2
  exit 1
fi

case "${MODE}" in
  loopback|local)
    if ! command -v route >/dev/null 2>&1 || ! command -v ipconfig >/dev/null 2>&1; then
      echo "Local mode currently supports macOS hosts with route/ipconfig." >&2
      exit 1
    fi

    DEFAULT_IF="$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')"
    if [ -z "${DEFAULT_IF}" ]; then
      echo "Failed to detect default interface." >&2
      exit 1
    fi

    HOST_IP="$(ipconfig getifaddr "${DEFAULT_IF}" 2>/dev/null || true)"
    if [ -z "${HOST_IP}" ]; then
      echo "Failed to detect IPv4 address for ${DEFAULT_IF}." >&2
      exit 1
    fi

    MODE_LABEL="${DEFAULT_IF}-local"
    ;;
  lan)
    if ! command -v route >/dev/null 2>&1 || ! command -v ipconfig >/dev/null 2>&1; then
      echo "LAN mode currently supports macOS hosts with route/ipconfig." >&2
      exit 1
    fi

    DEFAULT_IF="$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')"
    if [ -z "${DEFAULT_IF}" ]; then
      echo "Failed to detect default interface." >&2
      exit 1
    fi

    HOST_IP="$(ipconfig getifaddr "${DEFAULT_IF}" 2>/dev/null || true)"
    if [ -z "${HOST_IP}" ]; then
      echo "Failed to detect IPv4 address for ${DEFAULT_IF}." >&2
      exit 1
    fi

    MODE_LABEL="${DEFAULT_IF}"
    ;;
  *)
    echo "Usage: $0 [loopback|lan]" >&2
    exit 1
    ;;
esac

tmp="$(mktemp)"
awk -v ip="${HOST_IP}" '
  BEGIN { updated_rtp=0; updated_sip=0 }
  /^EXTERNAL_RTP_IP=/ { print "EXTERNAL_RTP_IP=" ip; updated_rtp=1; next }
  /^EXTERNAL_SIP_IP=/ { print "EXTERNAL_SIP_IP=" ip; updated_sip=1; next }
  { print }
  END {
    if (!updated_rtp) print "EXTERNAL_RTP_IP=" ip
    if (!updated_sip) print "EXTERNAL_SIP_IP=" ip
  }
' "${ENV_FILE}" > "${tmp}"

mv "${tmp}" "${ENV_FILE}"

echo "Updated EXTERNAL_RTP_IP and EXTERNAL_SIP_IP to ${HOST_IP} (${MODE_LABEL})"
