package posthogmcp

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const generatedSessionInactivity = 30 * time.Minute

type requestAttribution struct {
	sessionID      string
	conversationID string
	clientName     string
	clientVersion  string
	protocol       string
	userAgent      string
	vendorClient   string
	serverName     string
	serverVersion  string
	identity       *Identity
}

func (a *Analytics) resolveAttribution(request mcp.Request, conversationID string) requestAttribution {
	attribution := requestAttribution{}
	if validConversationID(conversationID) {
		attribution.conversationID = conversationID
		attribution.sessionID = deriveSessionID(conversationID)
	} else if protocolSessionID(request) != "" {
		attribution.sessionID = deriveSessionID(protocolSessionID(request))
	} else {
		attribution.sessionID = a.generatedSessionID(request)
	}
	stampRequestAttribution(&attribution, request)
	a.stampStoredAttribution(&attribution)
	stampMetaAttribution(&attribution, requestMetadata(request))
	return attribution
}

func (a *Analytics) generatedSessionID(request mcp.Request) string {
	if request != nil && request.GetSession() != nil {
		return a.generatedConnectionSessionID(request.GetSession())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if now.Sub(a.lastActivity) > generatedSessionInactivity {
		a.fallbackSession = newPrefixedID("ses")
	}
	a.lastActivity = now
	return a.fallbackSession
}

func (a *Analytics) generatedConnectionSessionID(connection mcp.Session) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if sessionID := a.generated[connection]; sessionID != "" {
		state := a.sessions[sessionID]
		if state != nil && now.Sub(state.lastActivity) <= generatedSessionInactivity {
			a.touchSequence++
			state.touched = a.touchSequence
			state.lastActivity = now
			return sessionID
		}
		delete(a.sessions, sessionID)
		delete(a.generated, connection)
	}
	sessionID := newPrefixedID("ses")
	state := a.touchSessionLocked(sessionID)
	state.connection = connection
	state.lastActivity = now
	a.generated[connection] = sessionID
	return sessionID
}

func protocolSessionID(request mcp.Request) string {
	if request == nil || request.GetSession() == nil {
		return ""
	}
	return strings.TrimSpace(request.GetSession().ID())
}

func validConversationID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 7
}
