package posthogmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lifecycleSnapshot struct {
	method         string
	request        mcp.Request
	result         mcp.Result
	err            error
	started        time.Time
	toolName       string
	arguments      map[string]any
	intent         string
	conversationID string
	missing        bool
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
	properties := map[string]any{
		PropertyDurationMS: float64(time.Since(snapshot.started).Microseconds()) / 1000,
		PropertyIsError:    snapshot.err != nil,
	}
	addLifecycleProperties(properties, snapshot)
	if snapshot.toolName != "" {
		properties[PropertyToolName] = snapshot.toolName
	}
	if snapshot.arguments != nil {
		properties[PropertyParameters] = snapshot.arguments
	}
	if snapshot.intent != "" {
		properties[PropertyIntent] = snapshot.intent
		properties[PropertyIntentSource] = "context_parameter"
	}
	if snapshot.conversationID != "" {
		properties[PropertyConversationID] = snapshot.conversationID
	}
	if snapshot.result != nil {
		properties[PropertyResponse] = snapshot.result
	}
	if snapshot.err != nil {
		properties[PropertyErrorMessage] = snapshot.err.Error()
	}
	capturer.captureEvent(ctx, Event{Event: eventName, Timestamp: snapshot.started, Properties: properties}, snapshot.request)
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
		if len(params.Arguments) > 0 {
			properties[PropertyParameters] = params.Arguments
		}
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
		"initialize":     EventInitialize,
		"tools/list":     EventToolsList,
		"tools/call":     EventToolCall,
		"resources/list": EventResourcesList,
		"resources/read": EventResourceRead,
		"prompts/list":   EventPromptsList,
		"prompts/get":    EventPromptGet,
	}[method]
}

func (a *Analytics) handleToolCall(ctx context.Context, state *middlewareState, next mcp.MethodHandler, request mcp.Request, started time.Time) (result mcp.Result, err error) {
	originalRequest := request
	a.discoverMissingTool(ctx, state, next, request)
	preparedRequest, name, arguments, ownership, conversation := prepareToolRequest(state, request, a.options.EnableConversationID)
	intent := a.resolveIntent(ctx, originalRequest, arguments, ownership)
	missing := a.options.ReportMissing && ownership.virtualMissing

	defer func() {
		if recovered := recover(); recovered != nil {
			a.emitLifecycle(ctx, lifecycleSnapshot{method: "tools/call", request: originalRequest, err: fmt.Errorf("panic: %v", recovered), started: started, toolName: name, arguments: arguments, intent: intent, conversationID: conversation.id, missing: missing})
			panic(recovered)
		}
		a.emitLifecycle(ctx, lifecycleSnapshot{method: "tools/call", request: originalRequest, result: result, err: err, started: started, toolName: name, arguments: arguments, intent: intent, conversationID: conversation.id, missing: missing})
	}()
	if missing {
		return applyConversationResult(missingCapabilityResult(), conversation, ownership), nil
	}
	result, err = next(ctx, "tools/call", preparedRequest)
	if err == nil {
		result = applyConversationResult(result, conversation, ownership)
	}
	return result, err
}

func (a *Analytics) resolveIntent(ctx context.Context, request mcp.Request, arguments map[string]any, ownership parameterOwnership) (intent string) {
	if ownership.context {
		if supplied, ok := arguments["context"].(string); ok {
			return supplied
		}
	}
	if a.options.IntentFallback == nil {
		return ""
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.safeLog("intent fallback failed", "error", recovered)
			intent = ""
		}
	}()
	resolved, err := a.options.IntentFallback(ctx, request)
	if err != nil {
		a.safeLog("intent fallback failed", "error", err)
		return ""
	}
	return resolved
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

func (a *Analytics) discoverMissingTool(ctx context.Context, state *middlewareState, next mcp.MethodHandler, request mcp.Request) {
	if !a.options.ReportMissing {
		return
	}
	serverRequest, ok := request.(*mcp.ServerRequest[*mcp.CallToolParamsRaw])
	if !ok || serverRequest.Params == nil || serverRequest.Params.Name != a.options.MissingCapabilityToolName {
		return
	}
	if _, known := state.get(serverRequest.Params.Name); known {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.safeLog("missing-capability discovery failed", "error", recovered)
		}
	}()
	listingRequest := &mcp.ServerRequest[*mcp.ListToolsParams]{
		Session: serverRequest.Session,
		Params:  &mcp.ListToolsParams{},
		Extra:   serverRequest.Extra,
	}
	result, err := next(ctx, "tools/list", listingRequest)
	if err != nil {
		a.safeLog("missing-capability discovery failed", "error", err)
		return
	}
	listing, ok := result.(*mcp.ListToolsResult)
	if !ok || listing == nil {
		return
	}
	for _, tool := range listing.Tools {
		if tool != nil && tool.Name == a.options.MissingCapabilityToolName {
			state.set(tool.Name, parameterOwnership{})
			return
		}
	}
	state.set(a.options.MissingCapabilityToolName, parameterOwnership{context: true, virtualMissing: true})
}

func (a *Analytics) safeLog(message string, args ...any) {
	defer func() { _ = recover() }()
	if a != nil && a.options.Logger != nil {
		a.options.Logger.Error(message, args...)
	}
}
