package cluster

import "fmt"

func nodeHeartbeatKey(nodeID string) string {
	return fmt.Sprintf("vbgw:node:%s:heartbeat", nodeID)
}

func nodeStateKey(nodeID string) string {
	return fmt.Sprintf("vbgw:node:%s:state", nodeID)
}

func nodeChannel(nodeID string) string {
	return fmt.Sprintf("vbgw:node:%s:cluster", nodeID)
}

func leaseKey(serviceName, slotID string) string {
	return fmt.Sprintf("vbgw:lease:service:%s:slot:%s", serviceName, slotID)
}

func leaseSessionKey(sessionID string) string {
	return fmt.Sprintf("vbgw:lease:session:%s", sessionID)
}
