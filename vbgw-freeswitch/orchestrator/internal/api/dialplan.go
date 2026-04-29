/**
 * @file dialplan.go
 * @description mod_xml_curl을 처리하기 위한 동적 라우팅 엔진 엔드포인트
 */

package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/routing"
)

// DialplanHandler handles mod_xml_curl dialplan requests.
type DialplanHandler struct {
	legacyAIRoutes map[string]struct{}
	runtime        *routing.Runtime
	resolver       *routing.Resolver
	capacityMgr    *capacity.Manager
}

// NewDialplanHandler creates a dialplan handler for the configured AI route numbers.
func NewDialplanHandler(aiRouteNumbers []string, runtime *routing.Runtime, capacityMgr *capacity.Manager) *DialplanHandler {
	routes := make(map[string]struct{}, len(aiRouteNumbers))
	for _, number := range aiRouteNumbers {
		if number != "" {
			routes[number] = struct{}{}
		}
	}
	if len(routes) == 0 {
		routes["9196"] = struct{}{}
	}
	if runtime != nil && runtime.Config != nil {
		for _, svc := range runtime.Config.Services {
			if !svc.Enabled {
				continue
			}
			for _, entry := range svc.EntryNums {
				delete(routes, entry)
			}
		}
	}
	var resolver *routing.Resolver
	if runtime != nil {
		resolver = runtime.Resolver
	}
	return &DialplanHandler{
		legacyAIRoutes: routes,
		runtime:        runtime,
		resolver:       resolver,
		capacityMgr:    capacityMgr,
	}
}

// GenerateDialplan handles POST /api/v1/fs/dialplan
func (h *DialplanHandler) GenerateDialplan(w http.ResponseWriter, r *http.Request) {
	// FreeSWITCH sends data as x-www-form-urlencoded
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Dump debug for incoming parameters
	callerID := r.FormValue("Caller-Caller-ID-Number")
	destNum := r.FormValue("Caller-Destination-Number")
	huntCtx := r.FormValue("Hunt-Context")
	sourceGateway := firstNonEmpty(
		r.FormValue("variable_sip_gateway_name"),
		r.FormValue("sip_gateway_name"),
		r.FormValue("Channel-Variable-sip_gateway_name"),
	)
	ingressStage := normalizeIngressStage(huntCtx)

	slog.Info("Dynamic Dialplan request", "caller_id", callerID, "dest", destNum, "context", huntCtx, "ingress_stage", ingressStage, "source_gateway", sourceGateway)

	if ingressStage != "default-policy" {
		metrics.RouteResolutionTotal.WithLabelValues("fallback").Inc()
		writeDialplanNotFound(w)
		return
	}

	if h.resolver != nil {
		if route, ok := h.resolver.Resolve(routing.ResolveInput{
			DestinationNumber: destNum,
			IngressStage:      ingressStage,
			SourceGateway:     sourceGateway,
		}); ok && route.RouteType == routing.RouteTypeAI {
			if h.capacityMgr != nil {
				if decision := h.capacityMgr.Preview(route.ServiceName); decision.Configured && !decision.Allowed {
					metrics.OverflowTotal.WithLabelValues(route.ServiceName, decision.OverflowPolicy).Inc()
					metrics.RouteResolutionTotal.WithLabelValues("overflow_precheck").Inc()
					switch decision.OverflowPolicy {
					case routing.OverflowBusy:
						writeOverflowDialplan(w, route.EntryNumber, route.ServiceName, route.RouteType, route.IngressStage, route.RoutingConfigVersion, decision.OverflowPolicy, decision.TransferTarget)
						return
					case routing.OverflowDirectTransfer:
						writeOverflowDialplan(w, route.EntryNumber, route.ServiceName, route.RouteType, route.IngressStage, route.RoutingConfigVersion, decision.OverflowPolicy, decision.TransferTarget)
						return
					case routing.OverflowQueue:
						writeQueueHoldDialplan(w, route.EntryNumber, route.ServiceName, route.RouteType, route.IngressStage, route.RoutingConfigVersion, decision.QueueAnnouncement)
						return
					case routing.OverflowFallbackHuman:
						writeHumanFallbackDialplan(w, route.EntryNumber, route.ServiceName, route.RouteType, route.IngressStage, route.RoutingConfigVersion, decision.TransferTarget)
						return
					}
				}
			}
			metrics.RouteResolutionTotal.WithLabelValues("policy_matched").Inc()
			writeAIDialplan(w, route.EntryNumber, route.ServiceName, route.RouteType, route.IngressStage, route.RoutingConfigVersion)
			return
		}
	}

	if _, ok := h.legacyAIRoutes[destNum]; ok {
		metrics.RouteResolutionTotal.WithLabelValues("legacy_matched").Inc()
		writeAIDialplan(w, destNum, "", routing.RouteTypeAI, ingressStage, 0)
		return
	}

	metrics.RouteResolutionTotal.WithLabelValues("fallback").Inc()
	writeDialplanNotFound(w)
}

func writeAIDialplan(w http.ResponseWriter, entryNumber, serviceName, routeType, ingressStage string, routingConfigVersion int) {
	routeExpr := regexp.QuoteMeta(entryNumber)
	serviceName = xmlEscapedValue(serviceName)
	routeType = xmlEscapedValue(routeType)
	ingressStage = xmlEscapedValue(ingressStage)
	xmlPayload := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<document type="freeswitch/xml">
  <section name="dialplan" description="Dynamic Routing API">
    <context name="default">
      <extension name="vbgw-ai-dynamic">
        <condition field="destination_number" expression="^%s$">
          <action application="set" data="vbgw_entry_number=%s"/>
          <action application="set" data="vbgw_service_name=%s"/>
          <action application="set" data="vbgw_route_type=%s"/>
          <action application="set" data="vbgw_ingress_stage=%s"/>
          <action application="set" data="vbgw_routing_config_version=%d"/>
          <action application="answer"/>
          <action application="park"/>
        </condition>
      </extension>
    </context>
  </section>
</document>`, routeExpr, entryNumber, serviceName, routeType, ingressStage, routingConfigVersion)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xmlPayload))
}

func writeOverflowDialplan(w http.ResponseWriter, entryNumber, serviceName, routeType, ingressStage string, routingConfigVersion int, overflowPolicy, transferTarget string) {
	routeExpr := regexp.QuoteMeta(entryNumber)
	serviceName = xmlEscapedValue(serviceName)
	routeType = xmlEscapedValue(routeType)
	ingressStage = xmlEscapedValue(ingressStage)
	overflowPolicy = xmlEscapedValue(overflowPolicy)
	transferTarget = xmlEscapedValue(transferTarget)

	terminalAction := `<action application="hangup" data="USER_BUSY"/>`
	if overflowPolicy == routing.OverflowDirectTransfer && transferTarget != "" {
		terminalAction = fmt.Sprintf(`<action application="transfer" data="%s XML default"/>`, transferTarget)
	}

	xmlPayload := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<document type="freeswitch/xml">
  <section name="dialplan" description="Dynamic Routing Overflow">
    <context name="default">
      <extension name="vbgw-ai-overflow">
        <condition field="destination_number" expression="^%s$">
          <action application="set" data="vbgw_entry_number=%s"/>
          <action application="set" data="vbgw_service_name=%s"/>
          <action application="set" data="vbgw_route_type=%s"/>
          <action application="set" data="vbgw_ingress_stage=%s"/>
          <action application="set" data="vbgw_routing_config_version=%d"/>
          <action application="set" data="vbgw_overflow_policy=%s"/>
          <action application="set" data="vbgw_overflow_target=%s"/>
          %s
        </condition>
      </extension>
    </context>
  </section>
</document>`, routeExpr, entryNumber, serviceName, routeType, ingressStage, routingConfigVersion, overflowPolicy, transferTarget, terminalAction)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xmlPayload))
}

func writeDialplanNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(dialplanNotFoundXML))
}

const dialplanNotFoundXML = `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<document type="freeswitch/xml">
  <section name="result">
    <result status="not found"/>
  </section>
</document>`

func normalizeIngressStage(huntCtx string) string {
	switch strings.TrimSpace(huntCtx) {
	case "", "default":
		return "default-policy"
	case "public", "public-admission":
		return "public-admission"
	default:
		return strings.TrimSpace(huntCtx)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func xmlEscapedValue(v string) string {
	replacer := strings.NewReplacer("&", "&amp;", "\"", "&quot;", "'", "&apos;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(v)
}

func writeQueueHoldDialplan(w http.ResponseWriter, entryNumber, serviceName, routeType, ingressStage string, routingConfigVersion int, queueAnnouncement string) {
	routeExpr := regexp.QuoteMeta(entryNumber)
	serviceName = xmlEscapedValue(serviceName)
	routeType = xmlEscapedValue(routeType)
	ingressStage = xmlEscapedValue(ingressStage)
	if queueAnnouncement == "" {
		queueAnnouncement = "silence_stream://50"
	}
	queueAnnouncement = xmlEscapedValue(queueAnnouncement)

	xmlPayload := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<document type="freeswitch/xml">
  <section name="dialplan" description="Dynamic Routing Queue Hold">
    <context name="default">
      <extension name="vbgw-ai-queue-hold">
        <condition field="destination_number" expression="^%s$">
          <action application="set" data="vbgw_entry_number=%s"/>
          <action application="set" data="vbgw_service_name=%s"/>
          <action application="set" data="vbgw_route_type=%s"/>
          <action application="set" data="vbgw_ingress_stage=%s"/>
          <action application="set" data="vbgw_routing_config_version=%d"/>
          <action application="set" data="vbgw_overflow_policy=queue"/>
          <action application="answer"/>
          <action application="playback" data="%s"/>
          <action application="park"/>
        </condition>
      </extension>
    </context>
  </section>
</document>`, routeExpr, entryNumber, serviceName, routeType, ingressStage, routingConfigVersion, queueAnnouncement)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xmlPayload))
}

func writeHumanFallbackDialplan(w http.ResponseWriter, entryNumber, serviceName, routeType, ingressStage string, routingConfigVersion int, transferTarget string) {
	routeExpr := regexp.QuoteMeta(entryNumber)
	serviceName = xmlEscapedValue(serviceName)
	routeType = xmlEscapedValue(routeType)
	ingressStage = xmlEscapedValue(ingressStage)
	transferTarget = xmlEscapedValue(transferTarget)

	terminalAction := `<action application="hangup" data="USER_BUSY"/>`
	if transferTarget != "" {
		terminalAction = fmt.Sprintf(`<action application="transfer" data="%s XML default"/>`, transferTarget)
	}

	xmlPayload := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<document type="freeswitch/xml">
  <section name="dialplan" description="Dynamic Routing Human Fallback">
    <context name="default">
      <extension name="vbgw-ai-human-fallback">
        <condition field="destination_number" expression="^%s$">
          <action application="set" data="vbgw_entry_number=%s"/>
          <action application="set" data="vbgw_service_name=%s"/>
          <action application="set" data="vbgw_route_type=%s"/>
          <action application="set" data="vbgw_ingress_stage=%s"/>
          <action application="set" data="vbgw_routing_config_version=%d"/>
          <action application="set" data="vbgw_overflow_policy=fallback_to_human"/>
          %s
        </condition>
      </extension>
    </context>
  </section>
</document>`, routeExpr, entryNumber, serviceName, routeType, ingressStage, routingConfigVersion, terminalAction)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xmlPayload))
}
