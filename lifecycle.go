package posthogmcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lifecycleSnapshot struct {
	method          string
	request         mcp.Request
	result          mcp.Result
	err             error
	started         time.Time
	toolName        string
	toolDescription string
	toolCategory    string
	arguments       map[string]any
	intent          string
	intentSource    string
	conversationID  string
	missing         bool
	attribution     requestAttribution
	identify        *Event
	failure         *toolFailure
}

type automaticEventCapturer interface {
	captureEvent(context.Context, Event, mcp.Request)
}

func (a *Analytics) emitLifecycle(ctx context.Context, snapshot lifecycleSnapshot) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.safeLog("analytics lifecycle capture failed", "error", recovered)
		}
	}()
	capturer, ok := any(a).(automaticEventCapturer)
	if !ok {
		return
	}
	eventName := lifecycleEventName(snapshot.method, snapshot.missing)
	if eventName == "" {
		return
	}
	ctx = withResolvedEventProperties(ctx, a.resolveEventProperties(ctx, snapshot.request))
	properties := map[string]any{
		PropertyDurationMS: float64(time.Since(snapshot.started).Microseconds()) / 1000,
		PropertyIsError:    snapshot.err != nil,
	}
	addLifecycleProperties(properties, snapshot)
	if snapshot.toolName != "" {
		properties[PropertyToolName] = snapshot.toolName
		properties[PropertyResourceName] = snapshot.toolName
	}
	if snapshot.toolDescription != "" {
		properties[PropertyToolDescription] = snapshot.toolDescription
	}
	if snapshot.toolCategory != "" {
		properties[PropertyToolCategory] = snapshot.toolCategory
	}
	parameters := snapshot.arguments
	if parameters == nil {
		parameters = buildCapturedRequestParameters(snapshot.method, snapshot.request)
	}
	if parameters != nil {
		properties[PropertyParameters] = parameters
	}
	if snapshot.intent != "" {
		properties[PropertyIntent] = snapshot.intent
		properties[PropertyIntentSource] = snapshot.intentSource
	}
	if snapshot.conversationID != "" {
		properties[PropertyConversationID] = snapshot.conversationID
	}
	if snapshot.result != nil {
		properties[PropertyResponse] = snapshot.result
	}
	if snapshot.err != nil {
		properties[PropertyErrorMessage] = safeErrorString(snapshot.err)
	}
	if snapshot.failure != nil {
		properties[PropertyIsError] = true
		properties[PropertyErrorType] = snapshot.failure.errorType
		properties[PropertyErrorMessage] = snapshot.failure.message
	}
	event := Event{Event: eventName, Timestamp: snapshot.started, Properties: properties}
	snapshot.attribution.applyToEvent(&event)
	if snapshot.identify != nil {
		identify := *snapshot.identify
		snapshot.attribution.applyToEvent(&identify)
		capturer.captureEvent(withRequestAttribution(ctx, snapshot.attribution), identify, snapshot.request)
	}
	var exception *Event
	if snapshot.method == "tools/call" {
		exception = applyToolFailure(&event, snapshot.failure)
	}
	attributedContext := withRequestAttribution(ctx, snapshot.attribution)
	capturer.captureEvent(attributedContext, event, snapshot.request)
	if exception != nil && !a.options.DisableExceptionAutocapture {
		capturer.captureEvent(attributedContext, *exception, snapshot.request)
	}
}

func addLifecycleProperties(properties map[string]any, snapshot lifecycleSnapshot) {
	switch params := snapshot.request.GetParams().(type) {
	case *mcp.InitializeParams:
		properties[PropertyProtocolVersion] = params.ProtocolVersion
		if params.ClientInfo != nil {
			properties[PropertyClientName] = params.ClientInfo.Name
			properties[PropertyClientVersion] = params.ClientInfo.Version
		}
	case *mcp.ReadResourceParams:
		properties[PropertyResourceName] = params.URI
	case *mcp.GetPromptParams:
		properties[PropertyResourceName] = params.Name
	}
	switch result := snapshot.result.(type) {
	case *mcp.InitializeResult:
		properties[PropertyProtocolVersion] = result.ProtocolVersion
		if result.ServerInfo != nil {
			properties[PropertyServerName] = result.ServerInfo.Name
			properties[PropertyServerVersion] = result.ServerInfo.Version
		}
	case *mcp.ListToolsResult:
		names := make([]string, 0, len(result.Tools))
		for _, tool := range result.Tools {
			if tool != nil && tool.Name != "" {
				names = append(names, tool.Name)
			}
		}
		if len(names) > 0 {
			properties[PropertyListedToolNames] = names
		}
	case *mcp.CallToolResult:
		if result.IsError {
			properties[PropertyIsError] = true
		}
	}
}

func lifecycleEventName(method string, missing bool) string {
	if missing {
		return EventMissingCapability
	}
	return map[string]string{
		"initialize":      EventInitialize,
		"server/discover": EventInitialize,
		"tools/list":      EventToolsList,
		"tools/call":      EventToolCall,
		"resources/list":  EventResourcesList,
		"resources/read":  EventResourceRead,
		"prompts/list":    EventPromptsList,
		"prompts/get":     EventPromptGet,
	}[method]
}

func (a *Analytics) resolveIntent(ctx context.Context, request mcp.Request, arguments map[string]any, ownership parameterOwnership) (intent, source string) {
	if ownership.context {
		if supplied, ok := arguments["context"].(string); ok {
			return supplied, "context_parameter"
		}
	}
	if a.options.IntentFallback == nil {
		return "", ""
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.safeLog("intent fallback failed", "error", recovered)
			intent = ""
			source = ""
		}
	}()
	resolved, err := a.options.IntentFallback(ctx, cloneRequestForCallback(request))
	if err != nil {
		a.safeLog("intent fallback failed", "error", err)
		return "", ""
	}
	if resolved == "" {
		return "", ""
	}
	return resolved, "inferred"
}

func buildCapturedToolParameters(name string, arguments map[string]any, ownership parameterOwnership) map[string]any {
	params := map[string]any{"name": name}
	if arguments != nil {
		cleaned := make(map[string]any, len(arguments))
		for key, value := range arguments {
			if (key == "context" && ownership.context) || (key == "conversation_id" && ownership.conversationID) {
				continue
			}
			cleaned[key] = value
		}
		params["arguments"] = cleaned
	}
	return map[string]any{"request": map[string]any{"method": "tools/call", "params": params}}
}

func buildCapturedRequestParameters(method string, request mcp.Request) map[string]any {
	if request == nil {
		return nil
	}
	captured := map[string]any{"method": method}
	if params := request.GetParams(); params != nil {
		wire, err := json.Marshal(params)
		if err == nil {
			var normalized any
			if json.Unmarshal(wire, &normalized) == nil {
				captured["params"] = normalized
			}
		}
	}
	return map[string]any{"request": captured}
}

func prepareToolRequest(state *middlewareState, request mcp.Request, conversationsEnabled bool) (mcp.Request, string, map[string]any, parameterOwnership, conversationResolution) {
	serverRequest, ok := request.(*mcp.ServerRequest[*mcp.CallToolParamsRaw])
	if !ok || serverRequest.Params == nil {
		return request, "", nil, parameterOwnership{}, conversationResolution{}
	}
	name := serverRequest.Params.Name
	ownership, _ := state.get(name)
	var arguments map[string]any
	if err := json.Unmarshal(serverRequest.Params.Arguments, &arguments); err != nil || arguments == nil {
		return request, name, nil, ownership, conversationResolution{}
	}
	conversation := resolveConversationID(conversationsEnabled && ownership.conversationID, arguments)
	cleaned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		if (key == "context" && ownership.context) || (key == "conversation_id" && ownership.conversationID) {
			continue
		}
		cleaned[key] = value
	}
	raw, err := json.Marshal(cleaned)
	if err != nil {
		return request, name, arguments, ownership, conversation
	}
	params := *serverRequest.Params
	params.Arguments = raw
	clone := *serverRequest
	clone.Params = &params
	return &clone, name, arguments, ownership, conversation
}

func (a *Analytics) safeLog(message string, args ...any) {
	defer func() { _ = recover() }()
	if a != nil && a.options.Logger != nil {
		a.options.Logger.Error(message, args...)
	}
}
