/**
 * @file commands.go
 * @description ESL uuid_* 명령 래퍼 — FreeSWITCH 콜 제어 API
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | 12개 ESL 명령 래퍼
 * ─────────────────────────────────────────
 */

package esl

import (
	"fmt"
	"strings"
)

// Originate starts an outbound call via the PBX gateway.
// P-07: callerID parameter for CID display (Korean telecom law).
// P-08: Returns bgapi Job-UUID; use CHANNEL_HANGUP event for SIP response code.
// P-11/Q-03: Tries pbx-main first, falls back to pbx-standby if configured.
func (c *Client) Originate(uuid, target, callerID string, useStandby bool) (string, error) {
	cidParam := ""
	if callerID != "" {
		cidParam = fmt.Sprintf(",origination_caller_id_number=%s,origination_caller_id_name=%s",
			callerID, callerID)
	}

	gateway := fmt.Sprintf("sofia/gateway/pbx-main/%s", target)
	// Q-03: Only add standby failover pipe if standby PBX is configured
	if useStandby {
		gateway += fmt.Sprintf("|sofia/gateway/pbx-standby/%s", target)
	}

	cmd := fmt.Sprintf(
		"originate {origination_uuid=%s%s,failure_causes=NORMAL_TEMPORARY_FAILURE,originate_timeout=30}%s &park()",
		uuid, cidParam, gateway,
	)
	return c.SendBgAPI(cmd)
}

// SendDtmf sends DTMF digits to a channel.
func (c *Client) SendDtmf(uuid, digits string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_send_dtmf %s %s", uuid, digits))
	return err
}

// Transfer performs a blind transfer.
func (c *Client) Transfer(uuid, target string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_transfer %s %s XML default", uuid, target))
	return err
}

// Bridge bridges two channels (1:1).
func (c *Client) Bridge(uuidA, uuidB string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_bridge %s %s", uuidA, uuidB))
	return err
}

// Unbridge parks a channel to detach from bridge.
func (c *Client) Unbridge(uuid string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_transfer %s -both park", uuid))
	return err
}

// RecordStart begins recording a channel.
func (c *Client) RecordStart(uuid, path string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_record %s start %s", uuid, path))
	return err
}

// RecordStop stops recording a channel.
func (c *Client) RecordStop(uuid string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_record %s stop", uuid))
	return err
}

// Break interrupts the current playback on a channel (barge-in).
func (c *Client) Break(uuid string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_break %s all", uuid))
	return err
}

// Kill terminates a channel.
func (c *Client) Kill(uuid string) error {
	_, err := c.SendAPI(fmt.Sprintf("uuid_kill %s", uuid))
	return err
}

// Dump returns all channel variables as key=value pairs.
func (c *Client) Dump(uuid string) (map[string]string, error) {
	resp, err := c.SendAPI(fmt.Sprintf("uuid_dump %s", uuid))
	if err != nil {
		return nil, err
	}

	// Check for FS API error responses
	if strings.Contains(resp, "-ERR") || strings.Contains(resp, "-USAGE") {
		return nil, fmt.Errorf("uuid_dump failed: %s", strings.TrimSpace(resp))
	}

	result := make(map[string]string)
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			result[line[:idx]] = line[idx+1:]
		}
	}
	return result, nil
}

// Pause sends fsctl pause to stop accepting new calls.
func (c *Client) Pause() error {
	_, err := c.SendAPI("fsctl pause")
	return err
}

// Resume sends fsctl resume to accept new calls again.
// Q-09: Called on Orchestrator startup to ensure FS is not stuck in paused state
// (e.g., after Orchestrator-only restart while FS kept running).
func (c *Client) Resume() error {
	_, err := c.SendAPI("fsctl resume")
	return err
}

// Hupall hangs up all active calls.
func (c *Client) Hupall() error {
	_, err := c.SendAPI("hupall")
	return err
}
