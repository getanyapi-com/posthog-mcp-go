package posthogmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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

	identity, ok := callIdentify(ctx, a.options.Identify, cloneRequestForCallback(request))
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
	if previous == nil {
		event.Properties[PropertyAnonDistinctID] = attribution.sessionID
	}
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
	if err != nil {
		return nil, false
	}
	return snapshotIdentity(identity)
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

func identitiesEqual(first, second *Identity) (equal bool) {
	defer func() {
		if recover() != nil {
			equal = false
		}
	}()
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
	cloned, _ := snapshotIdentity(identity)
	return cloned
}

func snapshotIdentity(identity *Identity) (snapshot *Identity, ok bool) {
	defer func() {
		if recover() != nil {
			snapshot, ok = nil, false
		}
	}()
	if identity == nil || strings.TrimSpace(identity.DistinctID) == "" {
		return nil, false
	}
	return &Identity{
		DistinctID: truncateUTF8(sanitizeString(identity.DistinctID), maxStringBytes),
		Properties: cloneIdentityProperties(identity.Properties),
		Groups:     cloneGroups(identity.Groups),
	}, true
}

func cloneIdentityProperties(properties posthog.Properties) posthog.Properties {
	if properties == nil {
		return nil
	}
	cloned, _ := sanitizeValue(properties, identityCloneDepth).(map[string]any)
	return posthog.Properties(cloned)
}

func cloneIdentityValue(value any, depth int) any {
	return sanitizeValue(value, max(identityCloneDepth-depth, 0))
}

func cloneGroups(groups posthog.Groups) posthog.Groups {
	if groups == nil {
		return nil
	}
	cloned, _ := sanitizeValue(groups, identityCloneDepth).(map[string]any)
	return posthog.Groups(cloned)
}
