package posthogmcp

import (
	"encoding/json"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const DefaultContextDescription = `Explain why you are calling this tool and how it fits into the user's overall goal. This parameter is used for analytics and user intent tracking. YOU MUST provide 15-25 words (count carefully). NEVER use first person ('I', 'we', 'you') - maintain third-person perspective. NEVER include sensitive information such as credentials, passwords, or personal data. Example (20 words): "Searching across the organization's repositories to find all open issues related to performance complaints and latency issues for team prioritization."`

const DefaultConversationIDDescription = "Echo the conversation_id from the server's previous response. The server provides it on the first call — never invent one, and do not issue parallel tool calls until you have it."

type parameterOwnership struct {
	context            bool
	conversationID     bool
	outputInstructions bool
	virtualMissing     bool
}

type middlewareState struct {
	mu        sync.RWMutex
	ownership map[string]parameterOwnership
}

func newMiddlewareState() *middlewareState {
	return &middlewareState{ownership: make(map[string]parameterOwnership)}
}

func (s *middlewareState) set(name string, ownership parameterOwnership) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownership[name] = ownership
}

func (s *middlewareState) get(name string) (parameterOwnership, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ownership, ok := s.ownership[name]
	return ownership, ok
}

func (a *Analytics) prepareToolsList(state *middlewareState, result mcp.Result) (prepared mcp.Result) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.safeLog("analytics schema preparation failed", "error", recovered)
			prepared = result
		}
	}()
	listing, ok := result.(*mcp.ListToolsResult)
	if !ok || listing == nil {
		return result
	}
	clone := *listing
	clone.Tools = make([]*mcp.Tool, 0, len(listing.Tools)+1)
	seenMissingName := false
	for _, original := range listing.Tools {
		if original == nil {
			clone.Tools = append(clone.Tools, nil)
			continue
		}
		tool := *original
		ownership := parameterOwnership{}
		if !a.options.DisableContextInjection {
			var injected bool
			tool.InputSchema, injected = injectInputParameter(tool.InputSchema, "context", a.contextDescription(), true)
			ownership.context = injected
		}
		if a.options.EnableConversationID {
			var injected bool
			tool.InputSchema, injected = injectInputParameter(tool.InputSchema, "conversation_id", DefaultConversationIDDescription, false)
			ownership.conversationID = injected
			tool.OutputSchema, ownership.outputInstructions = injectOutputInstructions(tool.OutputSchema)
		}
		if tool.Name == a.options.MissingCapabilityToolName {
			seenMissingName = true
		}
		state.set(tool.Name, ownership)
		clone.Tools = append(clone.Tools, &tool)
	}
	if a.options.ReportMissing && !seenMissingName {
		virtual := missingCapabilityTool(a.options.MissingCapabilityToolName)
		ownership := parameterOwnership{context: true, virtualMissing: true}
		if a.options.EnableConversationID {
			virtual.InputSchema, ownership.conversationID = injectInputParameter(virtual.InputSchema, "conversation_id", DefaultConversationIDDescription, false)
		}
		state.set(virtual.Name, ownership)
		clone.Tools = append(clone.Tools, virtual)
	} else if a.options.ReportMissing && seenMissingName {
		a.safeLog("cannot inject missing-capability tool because a real tool already uses its name", "tool", a.options.MissingCapabilityToolName)
	}
	return &clone
}

func (a *Analytics) contextDescription() string {
	if a.options.ContextDescription != "" {
		return a.options.ContextDescription
	}
	return DefaultContextDescription
}

func injectInputParameter(input any, name, description string, required bool) (any, bool) {
	schema, ok := cloneSchemaObject(input, true)
	if !ok || schema["$ref"] != nil || schema["oneOf"] != nil || schema["allOf"] != nil || schema["anyOf"] != nil {
		return input, false
	}
	properties, ok := schemaProperties(schema)
	if !ok {
		return input, false
	}
	if _, exists := properties[name]; exists {
		return input, false
	}
	properties[name] = map[string]any{"type": "string", "description": description}
	schema["properties"] = properties
	if additional, exists := schema["additionalProperties"]; exists && additional == false {
		delete(schema, "additionalProperties")
	}
	if required {
		schema["required"] = appendRequired(schema["required"], name)
	}
	return schema, true
}

func cloneSchemaObject(input any, allowNil bool) (map[string]any, bool) {
	if input == nil {
		if allowNil {
			return map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}, true
		}
		return nil, false
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil || schema == nil {
		return nil, false
	}
	return schema, true
}

func schemaProperties(schema map[string]any) (map[string]any, bool) {
	value, exists := schema["properties"]
	if !exists || value == nil {
		return map[string]any{}, true
	}
	properties, ok := value.(map[string]any)
	return properties, ok
}

func appendRequired(value any, name string) []any {
	items, _ := value.([]any)
	for _, item := range items {
		if item == name {
			return items
		}
	}
	return append(items, name)
}
