package posthogmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

func TestRequestAttributionUsesPublicSDKSessionClientProtocolAndHeaders(t *testing.T) {
	analytics := New(testEnqueueClient{}, nil)
	seen := make(chan requestAttribution, 1)
	server := mcp.NewServer(
		&mcp.Implementation{Name: "test-server", Version: "1.0.0"},
		&mcp.ServerOptions{GetSessionID: func() string { return "protocol-session" }},
	)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				seen <- analytics.resolveAttribution(request, "")
			}
			return next(ctx, method, request)
		}
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{JSONResponse: true},
	))
	t.Cleanup(httpServer.Close)

	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture-client", Version: "2.3.4"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if _, err := session.ListTools(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	attribution := <-seen
	if attribution.sessionID != deriveSessionID("protocol-session") {
		t.Errorf("session = %q, want deterministic protocol session", attribution.sessionID)
	}
	if attribution.clientName != "fixture-client" || attribution.clientVersion != "2.3.4" {
		t.Errorf("client = %q %q", attribution.clientName, attribution.clientVersion)
	}
	if attribution.protocol == "" {
		t.Error("protocol version is empty")
	}
	if attribution.userAgent != "fixture-agent/1.0" {
		t.Errorf("user-agent = %q", attribution.userAgent)
	}
	if attribution.vendorClient != "fixture-vendor" {
		t.Errorf("vendor client = %q", attribution.vendorClient)
	}
}

func TestRequestMetaOverridesConnectionClientAttribution(t *testing.T) {
	attribution := requestAttribution{clientName: "connection", clientVersion: "1", protocol: "old"}
	stampMetaAttribution(&attribution, map[string]any{
		metaClientInfoKey:      map[string]any{"name": "request-client", "version": "9"},
		metaProtocolVersionKey: "2026-07-28",
	})

	if attribution.clientName != "request-client" || attribution.clientVersion != "9" {
		t.Errorf("client = %q %q", attribution.clientName, attribution.clientVersion)
	}
	if attribution.protocol != "2026-07-28" {
		t.Errorf("protocol = %q", attribution.protocol)
	}
}

func TestInitializeResultAttributionIsScopedToItsSession(t *testing.T) {
	analytics := New(testEnqueueClient{}, nil)
	initialized := requestAttribution{sessionID: "ses_a", protocol: "requested"}
	analytics.rememberInitialize(&initialized, &mcp.InitializeResult{
		ProtocolVersion: "negotiated",
		ServerInfo:      &mcp.Implementation{Name: "server-a", Version: "1.2.3"},
	})

	sameSession := requestAttribution{sessionID: "ses_a"}
	analytics.stampStoredAttribution(&sameSession)
	if sameSession.serverName != "server-a" || sameSession.serverVersion != "1.2.3" {
		t.Errorf("server = %q %q", sameSession.serverName, sameSession.serverVersion)
	}
	if sameSession.protocol != "negotiated" {
		t.Errorf("protocol = %q", sameSession.protocol)
	}
	otherSession := requestAttribution{sessionID: "ses_b"}
	analytics.stampStoredAttribution(&otherSession)
	if otherSession.serverName != "" || otherSession.protocol != "" {
		t.Errorf("other session attribution leaked: %#v", otherSession)
	}
}

func TestRequestAttributionContextIsAnIsolatedSnapshot(t *testing.T) {
	original := requestAttribution{
		sessionID: "ses_a",
		identity: &Identity{
			DistinctID: "user-a",
			Properties: posthog.Properties{"plan": "free"},
		},
	}
	ctx := withRequestAttribution(context.Background(), original)
	original.identity.Properties["plan"] = "changed outside"

	first, ok := requestAttributionFromContext(ctx)
	if !ok || first.identity.Properties["plan"] != "free" {
		t.Fatalf("context snapshot = %#v, ok = %v", first, ok)
	}
	first.identity.Properties["plan"] = "changed by reader"
	second, _ := requestAttributionFromContext(ctx)
	if second.identity.Properties["plan"] != "free" {
		t.Fatalf("second context read = %#v", second.identity.Properties)
	}
}

type headerTransport struct {
	base http.RoundTripper
}

func (transport headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header.Set("User-Agent", "fixture-agent/1.0")
	cloned.Header.Set(vendorClientHeader, "fixture-vendor")
	return transport.base.RoundTrip(cloned)
}
