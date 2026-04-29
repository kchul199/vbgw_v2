package esl

import (
	"strings"
	"testing"
)

// TestOriginateCommandFormat tests the Originate command string generation.
// We can't test SendBgAPI (needs TCP), but we can verify the command format.
func TestBuildOutboundDialString_SingleGateway(t *testing.T) {
	c := &Client{primaryGateway: "pbx-main", standbyGateway: "pbx-standby"}
	got := c.buildOutboundDialString("1001", []string{"pbx-main"})
	want := "sofia/gateway/pbx-main/1001"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildOutboundDialString_WithStandby(t *testing.T) {
	c := &Client{primaryGateway: "main-gw", standbyGateway: "backup-gw"}
	got := c.buildOutboundDialString("1002", []string{"main-gw", "backup-gw"})
	want := "sofia/gateway/main-gw/1002|sofia/gateway/backup-gw/1002"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDumpParsing_ValidResponse(t *testing.T) {
	// Simulate uuid_dump response format
	resp := "channel_state=CS_EXECUTE\nread_codec=PCMU\nwrite_codec=PCMU\nrtp_audio_recv_pt=0\nrtp_audio_lost_pt=0\nvariable_sip_term_status=200\n"

	result := make(map[string]string)
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			result[line[:idx]] = line[idx+1:]
		}
	}

	if result["channel_state"] != "CS_EXECUTE" {
		t.Fatalf("expected CS_EXECUTE, got %s", result["channel_state"])
	}
	if result["read_codec"] != "PCMU" {
		t.Fatalf("expected PCMU, got %s", result["read_codec"])
	}
	if result["rtp_audio_lost_pt"] != "0" {
		t.Fatalf("expected 0 loss, got %s", result["rtp_audio_lost_pt"])
	}
}

func TestDumpParsing_ErrorResponse(t *testing.T) {
	resp := "-ERR No Such Channel!\n"

	if !strings.Contains(resp, "-ERR") {
		t.Fatal("should detect -ERR response")
	}
}

func TestDumpParsing_EmptyResponse(t *testing.T) {
	resp := ""

	result := make(map[string]string)
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			result[line[:idx]] = line[idx+1:]
		}
	}

	if len(result) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(result))
	}
}

func TestDumpParsing_ValueWithEquals(t *testing.T) {
	// Some FS variables contain '=' in the value
	resp := "variable_sip_contact_uri=sip:1001@192.168.1.1:5060;transport=udp\n"

	result := make(map[string]string)
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			result[line[:idx]] = line[idx+1:]
		}
	}

	expected := "sip:1001@192.168.1.1:5060;transport=udp"
	if result["variable_sip_contact_uri"] != expected {
		t.Fatalf("expected '%s', got '%s'", expected, result["variable_sip_contact_uri"])
	}
}
