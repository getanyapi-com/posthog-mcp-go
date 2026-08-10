package posthogmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

const identityCacheLimit = 1000
const identityCloneDepth = 10

type sessionState struct {
	identity        *Identity
	serverName      string
	serverVersion   string
	protocolVersion string
	connection      mcp.Session
	lastActivity    time.Time
	touched         uint64
}

func (a *Analytics) resolveIdentity(
	ctx context.Context,
	request mcp.Request,
	attribution *requestAttribution,
) *Event {
	started := a.now()
	previous := a.identityForSession(attribution.sessionID)
	attribution.identity = previous
	if a.options.Identify == nil {
		return nil
	}

	identity, ok := callIdentify(ctx, a.options.Identify, request)
	if !ok || identity == nil {
		return nil
	}
	merged, publish := a.updateIdentity(attribution.sessionID, identity)
	attribution.identity = merged
	if !publish {
		return nil
	}
	event := &Event{
		UUID:       newPrefixedID("evt")[4:],
		Event:      EventIdentify,
		Timestamp:  started,
		Properties: make(posthog.Properties),
	}
	event.Properties[PropertyDurationMS] = float64(a.now().Sub(started).Microseconds()) / 1000
	if resourceName := requestResourceName(request); resourceName != "" {
		event.Properties[PropertyResourceName] = resourceName
	}
	attribution.applyToEvent(event)
	return event
}

func requestResourceName(request mcp.Request) string {
	if request == nil {
		return ""
	}
	switch params := request.GetParams().(type) {
	case *mcp.CallToolParams:
		if params != nil {
			return params.Name
		}
	case *mcp.CallToolParamsRaw:
		if params != nil {
			return params.Name
		}
	}
	return ""
}

func callIdentify(ctx context.Context, identify IdentifyFunc, request mcp.Request) (identity *Identity, ok bool) {
	defer func() {
		if recover() != nil {
			identity = nil
			ok = false
		}
	}()
	identity, err := identify(ctx, request)
	return identity, err == nil
}

func (a *Analytics) updateIdentity(sessionID string, next *Identity) (*Identity, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.touchSessionLocked(sessionID)
	merged := mergeIdentity(state.identity, next)
	publish := state.identity == nil || !identitiesEqual(state.identity, merged)
	state.identity = cloneIdentity(merged)
	return cloneIdentity(merged), publish
}

func identitiesEqual(first, second *Identity) bool {
	if first == nil || second == nil || first.DistinctID != second.DistinctID {
		return first == nil && second == nil
	}
	firstJSON, firstErr := json.Marshal(struct {
		Properties posthog.Properties
		Groups     posthog.Groups
	}{first.Properties, first.Groups})
	secondJSON, secondErr := json.Marshal(struct {
		Properties posthog.Properties
		Groups     posthog.Groups
	}{second.Properties, second.Groups})
	return firstErr == nil && secondErr == nil && bytes.Equal(firstJSON, secondJSON)
}

func (a *Analytics) identityForSession(sessionID string) *Identity {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return nil
	}
	a.touchSequence++
	state.touched = a.touchSequence
	return cloneIdentity(state.identity)
}

func (a *Analytics) touchSessionLocked(sessionID string) *sessionState {
	state := a.sessions[sessionID]
	if state == nil {
		if len(a.sessions) >= identityCacheLimit {
			a.evictOldestSessionLocked()
		}
		state = &sessionState{}
		a.sessions[sessionID] = state
	}
	a.touchSequence++
	state.touched = a.touchSequence
	return state
}

func (a *Analytics) evictOldestSessionLocked() {
	var oldestID string
	oldestTouch := ^uint64(0)
	for sessionID, state := range a.sessions {
		if state.touched < oldestTouch {
			oldestID, oldestTouch = sessionID, state.touched
		}
	}
	if state := a.sessions[oldestID]; state != nil && state.connection != nil {
		delete(a.generated, state.connection)
	}
	delete(a.sessions, oldestID)
}

func (a *Analytics) sessionStateSize() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

func mergeIdentity(previous, next *Identity) *Identity {
	merged := cloneIdentity(next)
	if previous == nil {
		return merged
	}
	properties := cloneIdentityProperties(previous.Properties)
	for key, value := range next.Properties {
		properties[key] = cloneIdentityValue(value, 0)
	}
	merged.Properties = properties
	if next.Groups == nil {
		merged.Groups = cloneGroups(previous.Groups)
	}
	return merged
}

func cloneIdentity(identity *Identity) *Identity {
	if identity == nil {
		return nil
	}
	return &Identity{
		DistinctID: identity.DistinctID,
		Properties: cloneIdentityProperties(identity.Properties),
		Groups:     cloneGroups(identity.Groups),
	}
}

func cloneIdentityProperties(properties posthog.Properties) posthog.Properties {
	if properties == nil {
		return nil
	}
	cloned := make(posthog.Properties, len(properties))
	for key, value := range properties {
		cloned[key] = cloneIdentityValue(value, 0)
	}
	return cloned
}

func cloneIdentityValue(value any, depth int) any {
	if depth >= identityCloneDepth {
		return "[max depth]"
	}
	switch value := value.(type) {
	case posthog.Properties:
		cloned := make(posthog.Properties, len(value))
		for key, nested := range value {
			cloned[key] = cloneIdentityValue(nested, depth+1)
		}
		return cloned
	case posthog.Groups:
		cloned := make(posthog.Groups, len(value))
		for key, nested := range value {
			cloned[key] = cloneIdentityValue(nested, depth+1)
		}
		return cloned
	case map[string]any:
		cloned := make(map[string]any, len(value))
		for key, nested := range value {
			cloned[key] = cloneIdentityValue(nested, depth+1)
		}
		return cloned
	case []any:
		cloned := make([]any, len(value))
		for index, nested := range value {
			cloned[index] = cloneIdentityValue(nested, depth+1)
		}
		return cloned
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}

func cloneGroups(groups posthog.Groups) posthog.Groups {
	if groups == nil {
		return nil
	}
	cloned := make(posthog.Groups, len(groups))
	for key, value := range groups {
		cloned[key] = cloneIdentityValue(value, 0)
	}
	return cloned
}
