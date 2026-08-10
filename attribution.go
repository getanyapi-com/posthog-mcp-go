package posthogmcp

import (
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

const (
	metaClientInfoKey      = "io.modelcontextprotocol/clientInfo"
	metaProtocolVersionKey = "io.modelcontextprotocol/protocolVersion"
	vendorClientHeader     = "x-anthropic-client"
)

func stampRequestAttribution(attribution *requestAttribution, request mcp.Request) {
	if request == nil {
		return
	}
	stampInitializeAttribution(attribution, initializeParams(request))
	extra := request.GetExtra()
	if extra != nil {
		attribution.userAgent = cleanHeader(extra.Header, "user-agent")
		attribution.vendorClient = cleanHeader(extra.Header, vendorClientHeader)
	}
}

func requestMetadata(request mcp.Request) map[string]any {
	if request == nil {
		return nil
	}
	return requestMeta(request)
}

func requestMeta(request mcp.Request) (meta map[string]any) {
	defer func() {
		if recover() != nil {
			meta = nil
		}
	}()
	params := request.GetParams()
	if params == nil {
		return nil
	}
	return params.GetMeta()
}

func initializeParams(request mcp.Request) *mcp.InitializeParams {
	if params, ok := request.GetParams().(*mcp.InitializeParams); ok {
		return params
	}
	session, ok := request.GetSession().(*mcp.ServerSession)
	if !ok {
		return nil
	}
	return session.InitializeParams()
}

func stampInitializeAttribution(attribution *requestAttribution, params *mcp.InitializeParams) {
	if params == nil {
		return
	}
	if params.ClientInfo != nil {
		attribution.clientName = params.ClientInfo.Name
		attribution.clientVersion = params.ClientInfo.Version
	}
	attribution.protocol = params.ProtocolVersion
}

func stampMetaAttribution(attribution *requestAttribution, meta map[string]any) {
	if info, ok := meta[metaClientInfoKey].(map[string]any); ok {
		if name, ok := nonblankString(info["name"]); ok {
			attribution.clientName = name
		}
		if version, ok := nonblankString(info["version"]); ok {
			attribution.clientVersion = version
		}
	}
	if protocol, ok := nonblankString(meta[metaProtocolVersionKey]); ok {
		attribution.protocol = protocol
	}
}

func cleanHeader(header http.Header, name string) string {
	return strings.TrimSpace(header.Get(name))
}

func nonblankString(value any) (string, bool) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	return text, ok && text != ""
}

func (attribution requestAttribution) applyToEvent(event *Event) {
	if event.Properties == nil {
		event.Properties = make(posthog.Properties)
	}
	event.Properties[PropertySessionID] = attribution.sessionID
	setNonblank(event.Properties, PropertyConversationID, attribution.conversationID)
	setNonblank(event.Properties, PropertyClientName, attribution.clientName)
	setNonblank(event.Properties, PropertyClientVersion, attribution.clientVersion)
	setNonblank(event.Properties, PropertyProtocolVersion, attribution.protocol)
	setNonblank(event.Properties, PropertyClientUserAgent, attribution.userAgent)
	setNonblank(event.Properties, PropertyVendorClient, attribution.vendorClient)
	setNonblank(event.Properties, PropertyServerName, attribution.serverName)
	setNonblank(event.Properties, PropertyServerVersion, attribution.serverVersion)
	if attribution.identity == nil || attribution.identity.DistinctID == "" {
		event.DistinctID = attribution.sessionID
		event.Properties["$process_person_profile"] = false
		return
	}
	event.DistinctID = attribution.identity.DistinctID
	if len(attribution.identity.Properties) > 0 {
		event.Properties["$set"] = cloneProperties(attribution.identity.Properties)
	}
	event.Groups = cloneGroups(attribution.identity.Groups)
}

func setNonblank(properties posthog.Properties, key, value string) {
	if value != "" {
		properties[key] = value
	}
}

func (a *Analytics) rememberInitialize(attribution *requestAttribution, result *mcp.InitializeResult) {
	if result == nil {
		return
	}
	if result.ServerInfo != nil {
		attribution.serverName = result.ServerInfo.Name
		attribution.serverVersion = result.ServerInfo.Version
	}
	if result.ProtocolVersion != "" {
		attribution.protocol = result.ProtocolVersion
	}
	a.mu.Lock()
	state := a.touchSessionLocked(attribution.sessionID)
	state.serverName = attribution.serverName
	state.serverVersion = attribution.serverVersion
	state.protocolVersion = attribution.protocol
	a.mu.Unlock()
}

func (a *Analytics) stampStoredAttribution(attribution *requestAttribution) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[attribution.sessionID]
	if state == nil {
		return
	}
	a.touchSequence++
	state.touched = a.touchSequence
	attribution.serverName = state.serverName
	attribution.serverVersion = state.serverVersion
	if state.protocolVersion != "" {
		attribution.protocol = state.protocolVersion
	}
}
