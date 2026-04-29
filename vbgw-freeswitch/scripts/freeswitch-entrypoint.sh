#!/bin/sh
set -eu

CONF_DIR="${FS_CONF_DIR:-/etc/freeswitch}"
PROFILE_DIR="$CONF_DIR/sip_profiles/external"

xml_escape() {
  printf '%s' "${1:-}" | sed \
    -e 's/&/\&amp;/g' \
    -e 's/"/\&quot;/g' \
    -e "s/'/\&apos;/g" \
    -e 's/</\&lt;/g' \
    -e 's/>/\&gt;/g'
}

sed_escape() {
  printf '%s' "${1:-}" | sed -e 's/[\/&]/\\&/g'
}

append_param() {
  key="$1"
  value="$2"
  if [ -n "$value" ]; then
    printf '    <param name="%s" value="%s"/>\n' "$key" "$(xml_escape "$value")"
  fi
}

set_vars_xml_value() {
  key="$1"
  value="$2"
  file="$CONF_DIR/vars.xml"

  [ -f "$file" ] || return 0

  escaped_value="$(sed_escape "$value")"
  sed -i "s#data=\"$key=[^\"]*\"#data=\"$key=$escaped_value\"#g" "$file"
}

set_vars_xml_value "default_password" "${ESL_PASSWORD:-ClueCon}"
set_vars_xml_value "esl_password" "${ESL_PASSWORD:-ClueCon}"
set_vars_xml_value "external_rtp_ip" "${EXTERNAL_RTP_IP:-auto-nat}"
set_vars_xml_value "external_sip_ip" "${EXTERNAL_SIP_IP:-auto-nat}"
set_vars_xml_value "internal_local_network_acl" "${INTERNAL_LOCAL_NETWORK_ACL:-localnet.auto}"
set_vars_xml_value "vbgw_localnet_cidr" "${VBGW_LOCALNET_CIDR:-127.0.0.0/8}"
set_vars_xml_value "audio_fork_scheme" "${AUDIO_FORK_SCHEME:-ws}"
set_vars_xml_value "bridge_host" "${BRIDGE_HOST:-vbgw-bridge}"
set_vars_xml_value "bridge_ws_port" "${BRIDGE_WS_PORT:-8090}"
set_vars_xml_value "xml_curl_dialplan_url" "${XML_CURL_DIALPLAN_URL:-http://vbgw-orchestrator:8080/api/v1/fs/dialplan}"
set_vars_xml_value "global_codec_prefs" "${INBOUND_CODEC_PREFS:-PCMU,PCMA,G722,OPUS}"
set_vars_xml_value "outbound_codec_prefs" "${OUTBOUND_CODEC_PREFS:-PCMU,PCMA,G722,OPUS}"

render_gateway() {
  name="$1"
  enabled="$2"
  register="$3"
  proxy="$4"
  realm="$5"
  username="$6"
  password="$7"
  extension="$8"
  from_user="$9"
  from_domain="${10}"
  register_proxy="${11}"
  expire_seconds="${12}"
  retry_seconds="${13}"
  ping_seconds="${14}"
  register_transport="${15}"
  caller_id_in_from="${16}"
  contact_params="${17}"
  extension_in_contact="${18}"

  file="$PROFILE_DIR/$name.xml"

  if [ "$enabled" != "true" ] || [ -z "$proxy" ]; then
    rm -f "$file"
    return
  fi

  if [ -z "$realm" ]; then
    realm="$proxy"
  fi
  if [ -z "$from_user" ]; then
    from_user="$username"
  fi
  if [ -z "$from_domain" ]; then
    from_domain="$realm"
  fi
  if [ -z "$extension" ]; then
    extension="$username"
  fi
  if [ -z "$register_proxy" ]; then
    register_proxy="$proxy"
  fi
  if [ -z "$expire_seconds" ]; then
    expire_seconds="60"
  fi
  if [ -z "$retry_seconds" ]; then
    retry_seconds="30"
  fi
  if [ -z "$ping_seconds" ]; then
    ping_seconds="25"
  fi
  if [ -z "$caller_id_in_from" ]; then
    caller_id_in_from="false"
  fi
  if [ -z "$extension_in_contact" ]; then
    extension_in_contact="true"
  fi
  if [ -z "$register_transport" ]; then
    register_transport="udp"
  fi

  mkdir -p "$PROFILE_DIR"
  {
    printf '<include>\n'
    printf '  <gateway name="%s">\n' "$(xml_escape "$name")"
    append_param proxy "$proxy"
    append_param realm "$realm"
    append_param username "$username"
    append_param password "$password"
    append_param extension "$extension"
    append_param from-user "$from_user"
    append_param from-domain "$from_domain"
    append_param register-proxy "$register_proxy"
    append_param expire-seconds "$expire_seconds"
    append_param retry-seconds "$retry_seconds"
    append_param ping "$ping_seconds"
    append_param register-transport "$register_transport"
    append_param caller-id-in-from "$caller_id_in_from"
    append_param contact-params "$contact_params"
    append_param extension-in-contact "$extension_in_contact"
    append_param register "$register"
    printf '  </gateway>\n'
    printf '</include>\n'
  } >"$file"
}

render_gateway \
  "pbx-main" \
  "${PBX_INTERCONNECT_ENABLED:-false}" \
  "${PBX_MAIN_REGISTER:-true}" \
  "${PBX_MAIN_PROXY:-}" \
  "${PBX_MAIN_REALM:-}" \
  "${PBX_MAIN_USERNAME:-}" \
  "${PBX_MAIN_PASSWORD:-}" \
  "${PBX_MAIN_EXTENSION:-}" \
  "${PBX_MAIN_FROM_USER:-}" \
  "${PBX_MAIN_FROM_DOMAIN:-}" \
  "${PBX_MAIN_REGISTER_PROXY:-}" \
  "${PBX_MAIN_EXPIRE_SECONDS:-}" \
  "${PBX_MAIN_RETRY_SECONDS:-}" \
  "${PBX_MAIN_PING_SECONDS:-}" \
  "${PBX_MAIN_REGISTER_TRANSPORT:-}" \
  "${PBX_MAIN_CALLER_ID_IN_FROM:-}" \
  "${PBX_MAIN_CONTACT_PARAMS:-}" \
  "${PBX_MAIN_EXTENSION_IN_CONTACT:-}"

render_gateway \
  "pbx-standby" \
  "${PBX_STANDBY_ENABLED:-false}" \
  "${PBX_STANDBY_REGISTER:-true}" \
  "${PBX_STANDBY_PROXY:-}" \
  "${PBX_STANDBY_REALM:-}" \
  "${PBX_STANDBY_USERNAME:-}" \
  "${PBX_STANDBY_PASSWORD:-}" \
  "${PBX_STANDBY_EXTENSION:-}" \
  "${PBX_STANDBY_FROM_USER:-}" \
  "${PBX_STANDBY_FROM_DOMAIN:-}" \
  "${PBX_STANDBY_REGISTER_PROXY:-}" \
  "${PBX_STANDBY_EXPIRE_SECONDS:-}" \
  "${PBX_STANDBY_RETRY_SECONDS:-}" \
  "${PBX_STANDBY_PING_SECONDS:-}" \
  "${PBX_STANDBY_REGISTER_TRANSPORT:-}" \
  "${PBX_STANDBY_CALLER_ID_IN_FROM:-}" \
  "${PBX_STANDBY_CONTACT_PARAMS:-}" \
  "${PBX_STANDBY_EXTENSION_IN_CONTACT:-}"

exec /usr/local/freeswitch/bin/freeswitch \
  -nc \
  -nf \
  -nonat \
  -conf "$CONF_DIR" \
  -log /usr/local/freeswitch/log \
  -db /usr/local/freeswitch/db
