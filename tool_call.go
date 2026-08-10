package posthogmcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (a *Analytics) handleToolCall(ctx context.Context, state *middlewareState, next mcp.MethodHandler, request mcp.Request, started time.Time) (result mcp.Result, err error) {
	originalRequest := request
	preparedRequest, name, arguments, ownership, conversation := prepareToolRequest(state, request, a.options.EnableConversationID)
	probeMissing := false
	if a.options.ReportMissing && name == a.options.MissingCapabilityToolName {
		if listedOwnership, known := state.get(name); !known || listedOwnership.virtualMissing {
			probeMissing = true
			preparedRequest = originalRequest
			ownership = parameterOwnership{}
			conversation = conversationResolution{}
		}
	}
	var intent, intentSource string
	if !probeMissing {
		intent, intentSource = a.resolveIntent(ctx, originalRequest, arguments, ownership)
	}
	parameters := buildCapturedToolParameters(name, arguments, ownership)
	missing := false
	attribution := a.resolveAttribution(originalRequest, conversation.id)
	var identify *Event
	if !probeMissing {
		identify = a.resolveIdentity(ctx, originalRequest, &attribution)
	}
	ctx = withRequestAttribution(ctx, attribution)

	defer func() {
		if recovered := recover(); recovered != nil {
			if probeMissing && intent == "" {
				intent, intentSource = a.resolveIntent(ctx, originalRequest, arguments, ownership)
			}
			if probeMissing {
				identify = a.resolveIdentity(ctx, originalRequest, &attribution)
				ctx = withRequestAttribution(ctx, attribution)
			}
			a.emitLifecycle(ctx, lifecycleSnapshot{method: "tools/call", request: originalRequest, started: started, toolName: name, toolDescription: ownership.description, toolCategory: ownership.category, arguments: parameters, intent: intent, intentSource: intentSource, conversationID: conversation.id, missing: missing, attribution: attribution, identify: identify, failure: classifyPanic(recovered)})
			panic(recovered)
		}
		a.emitLifecycle(ctx, lifecycleSnapshot{method: "tools/call", request: originalRequest, result: result, err: err, started: started, toolName: name, toolDescription: ownership.description, toolCategory: ownership.category, arguments: parameters, intent: intent, intentSource: intentSource, conversationID: conversation.id, missing: missing, attribution: attribution, identify: identify, failure: classifyToolFailure(result, err)})
	}()
	result, err = next(ctx, "tools/call", preparedRequest)
	if probeMissing {
		if isUnknownToolError(name, result, err) {
			listedOwnership, known := state.get(name)
			if !known || !listedOwnership.virtualMissing {
				listedOwnership = parameterOwnership{context: true, virtualMissing: true}
			}
			ownership = listedOwnership
			conversation = resolveConversationID(a.options.EnableConversationID && ownership.conversationID, arguments)
			parameters = buildCapturedToolParameters(name, arguments, ownership)
			intent, intentSource = a.resolveIntent(ctx, originalRequest, arguments, ownership)
			resolvedAttribution := a.resolveAttribution(originalRequest, conversation.id)
			attribution = resolvedAttribution
			missing = true
			result, err = missingCapabilityResult(), nil
		} else {
			intent, intentSource = a.resolveIntent(ctx, originalRequest, arguments, ownership)
		}
		identify = a.resolveIdentity(ctx, originalRequest, &attribution)
		ctx = withRequestAttribution(ctx, attribution)
	}
	if err == nil && (!probeMissing || missing) {
		result = applyConversationResult(result, conversation, ownership)
	}
	return result, err
}
