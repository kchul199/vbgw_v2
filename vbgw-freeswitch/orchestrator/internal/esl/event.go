/**
 * @file event.go
 * @description ESL 이벤트 파싱 (Content-Type: text/event-plain)
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | ESL 이벤트 파서
 * ─────────────────────────────────────────
 */

package esl

import (
	"net/url"
	"strconv"
	"strings"
)

// Event represents a parsed FreeSWITCH ESL event.
type Event struct {
	Headers map[string]string
	Body    string
}

// Get returns a header value, URL-decoded.
func (e *Event) Get(key string) string {
	v := e.Headers[key]
	if decoded, err := url.QueryUnescape(v); err == nil {
		return decoded
	}
	return v
}

// Name returns the Event-Name header.
func (e *Event) Name() string {
	return e.Get("Event-Name")
}

// UUID returns the Unique-ID (FS channel UUID).
func (e *Event) UUID() string {
	return e.Get("Unique-ID")
}

// CallerID returns the Caller-Caller-ID-Number.
func (e *Event) CallerID() string {
	return e.Get("Caller-Caller-ID-Number")
}

// DestNumber returns the Caller-Destination-Number.
func (e *Event) DestNumber() string {
	return e.Get("Caller-Destination-Number")
}

// SourceGateway returns the source gateway if present on the channel.
func (e *Event) SourceGateway() string {
	for _, key := range []string{"variable_sip_gateway_name", "sip_gateway_name"} {
		if value := e.Get(key); value != "" {
			return value
		}
	}
	return ""
}

// RouteEntryNumber returns the logical entry number chosen by the dialplan.
func (e *Event) RouteEntryNumber() string {
	return e.Get("variable_vbgw_entry_number")
}

// RouteServiceName returns the logical service name chosen by the dialplan.
func (e *Event) RouteServiceName() string {
	return e.Get("variable_vbgw_service_name")
}

// RouteType returns the route type chosen by the dialplan.
func (e *Event) RouteType() string {
	return e.Get("variable_vbgw_route_type")
}

// RouteIngressStage returns the ingress stage chosen by the dialplan.
func (e *Event) RouteIngressStage() string {
	return e.Get("variable_vbgw_ingress_stage")
}

// RoutingConfigVersion returns the routing config version chosen by the dialplan.
func (e *Event) RoutingConfigVersion() int {
	v := e.Get("variable_vbgw_routing_config_version")
	n, _ := strconv.Atoi(v)
	return n
}

// RouteOverflowPolicy returns the overflow policy chosen by the dialplan.
func (e *Event) RouteOverflowPolicy() string {
	return e.Get("variable_vbgw_overflow_policy")
}

// RouteOverflowTarget returns the overflow transfer target chosen by the dialplan.
func (e *Event) RouteOverflowTarget() string {
	return e.Get("variable_vbgw_overflow_target")
}

// DtmfDigit returns the DTMF-Digit header.
func (e *Event) DtmfDigit() string {
	return e.Get("DTMF-Digit")
}

// SipTermStatus returns the SIP termination status code (e.g., "200", "486", "603").
func (e *Event) SipTermStatus() string {
	return e.Get("variable_sip_term_status")
}

// HangupCause returns the Hangup-Cause header.
func (e *Event) HangupCause() string {
	return e.Get("Hangup-Cause")
}

// SubClass returns the Event-Subclass for CUSTOM events.
func (e *Event) SubClass() string {
	return e.Get("Event-Subclass")
}

// ParseEvent parses a text/event-plain formatted ESL event.
// Format: "Header: Value\n" lines, then "\n", then optional body.
func ParseEvent(data string) *Event {
	evt := &Event{Headers: make(map[string]string)}

	parts := strings.SplitN(data, "\n\n", 2)
	headerBlock := parts[0]
	if len(parts) > 1 {
		evt.Body = parts[1]
	}

	for _, line := range strings.Split(headerBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ": ")
		if idx < 0 {
			continue
		}
		key := line[:idx]
		val := line[idx+2:]
		evt.Headers[key] = val
	}

	return evt
}
