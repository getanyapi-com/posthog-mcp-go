package posthogmcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

func TestIdentityPublishesFirstAndMaterialChangesOnly(t *testing.T) {
	identities := []*Identity{
		{DistinctID: "user-1", Properties: posthog.Properties{"plan": "free", "name": "Ada"}},
		{DistinctID: "user-1", Properties: posthog.Properties{"plan": "free"}},
		{DistinctID: "user-1", Properties: posthog.Properties{"plan": "pro"}},
		{DistinctID: "user-1", Properties: posthog.Properties{"plan": "pro"}},
	}
	call := 0
	analytics := New(testEnqueueClient{}, &Options{Identify: func(context.Context, mcp.Request) (*Identity, error) {
		identity := identities[call]
		call++
		return identity, nil
	}})
	attribution := requestAttribution{sessionID: "ses_fixture"}

	var published []*Event
	for range identities {
		published = append(published, analytics.resolveIdentity(context.Background(), nil, &attribution))
	}
	if published[0] == nil || published[1] != nil || published[2] == nil || published[3] != nil {
		t.Fatalf("identify publication pattern = %#v, want publish/dedupe/publish/dedupe", published)
	}
	if attribution.identity.Properties["name"] != "Ada" || attribution.identity.Properties["plan"] != "pro" {
		t.Errorf("merged identity = %#v", attribution.identity.Properties)
	}
	if published[2].Properties["$set"].(posthog.Properties)["plan"] != "pro" {
		t.Errorf("changed identify event = %#v", published[2].Properties)
	}
}

func TestIdentityHookFailuresKeepLastKnownIdentity(t *testing.T) {
	mode := 0
	analytics := New(testEnqueueClient{}, &Options{Identify: func(context.Context, mcp.Request) (*Identity, error) {
		mode++
		switch mode {
		case 1:
			return &Identity{DistinctID: "stable"}, nil
		case 2:
			return nil, errors.New("identify failed")
		default:
			panic("identify panic")
		}
	}})

	for range 3 {
		attribution := requestAttribution{sessionID: "ses_fixture"}
		_ = analytics.resolveIdentity(context.Background(), nil, &attribution)
		if attribution.identity == nil || attribution.identity.DistinctID != "stable" {
			t.Errorf("identity after hook failure = %#v", attribution.identity)
		}
	}
}

func TestIdentityCacheIsBoundedAndLeastRecentlyUsed(t *testing.T) {
	analytics := New(testEnqueueClient{}, &Options{Identify: func(ctx context.Context, _ mcp.Request) (*Identity, error) {
		return &Identity{DistinctID: ctx.Value(identityContextKey{}).(string)}, nil
	}})
	for index := range identityCacheLimit {
		sessionID := fmt.Sprintf("ses_%04d", index)
		attribution := requestAttribution{sessionID: sessionID}
		ctx := context.WithValue(context.Background(), identityContextKey{}, sessionID)
		analytics.resolveIdentity(ctx, nil, &attribution)
	}
	analytics.identityForSession("ses_0000")
	extra := requestAttribution{sessionID: "ses_extra"}
	analytics.resolveIdentity(context.WithValue(context.Background(), identityContextKey{}, "ses_extra"), nil, &extra)

	if got := analytics.sessionStateSize(); got != identityCacheLimit {
		t.Fatalf("cache size = %d, want %d", got, identityCacheLimit)
	}
	if analytics.identityForSession("ses_0001") != nil {
		t.Error("least recently used identity was not evicted")
	}
	if analytics.identityForSession("ses_0000") == nil {
		t.Error("recently used identity was evicted")
	}
}

func TestConcurrentIdentityAttributionDoesNotCrossSessions(t *testing.T) {
	analytics := New(testEnqueueClient{}, &Options{Identify: func(ctx context.Context, _ mcp.Request) (*Identity, error) {
		label := ctx.Value(identityContextKey{}).(string)
		return &Identity{DistinctID: "user-" + label, Properties: posthog.Properties{"label": label}}, nil
	}})
	const workers = 100
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := range workers {
		go func() {
			defer wait.Done()
			label := fmt.Sprintf("%d", index)
			attribution := requestAttribution{sessionID: "ses_" + label}
			ctx := context.WithValue(context.Background(), identityContextKey{}, label)
			analytics.resolveIdentity(ctx, nil, &attribution)
			if attribution.identity.DistinctID != "user-"+label {
				t.Errorf("session %s identity = %q", label, attribution.identity.DistinctID)
			}
		}()
	}
	wait.Wait()
}

func TestAnonymousAttributionOptsOutOfPersonProfiles(t *testing.T) {
	event := Event{Properties: posthog.Properties{}}
	requestAttribution{sessionID: "ses_anon"}.applyToEvent(&event)

	if event.DistinctID != "ses_anon" {
		t.Errorf("distinct id = %q", event.DistinctID)
	}
	if event.Properties["$process_person_profile"] != false {
		t.Errorf("person processing = %#v", event.Properties["$process_person_profile"])
	}
}

func TestIdentityStateDoesNotRetainNestedHookMutations(t *testing.T) {
	nested := map[string]any{"role": "member"}
	returned := &Identity{DistinctID: "user", Properties: posthog.Properties{"profile": nested}}
	analytics := New(testEnqueueClient{}, &Options{Identify: func(context.Context, mcp.Request) (*Identity, error) {
		return returned, nil
	}})
	attribution := requestAttribution{sessionID: "ses_fixture"}
	analytics.resolveIdentity(context.Background(), nil, &attribution)
	nested["role"] = "owner"

	stored := analytics.identityForSession("ses_fixture")
	profile := stored.Properties["profile"].(map[string]any)
	if profile["role"] != "member" {
		t.Fatalf("stored nested identity = %#v", profile)
	}
}

func TestIdentityHookCanReturnCyclicPropertiesWithoutBreakingRequest(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	analytics := New(testEnqueueClient{}, &Options{Identify: func(context.Context, mcp.Request) (*Identity, error) {
		return &Identity{DistinctID: "user", Properties: posthog.Properties{"cycle": cycle}}, nil
	}})
	attribution := requestAttribution{sessionID: "ses_fixture"}

	if event := analytics.resolveIdentity(context.Background(), nil, &attribution); event == nil {
		t.Fatal("first cyclic identity did not produce identify event")
	}
	if attribution.identity == nil || attribution.identity.DistinctID != "user" {
		t.Fatalf("attribution = %#v", attribution)
	}
}

func TestIdentityResolutionThroughRealSDKToolRequest(t *testing.T) {
	analytics := New(testEnqueueClient{}, &Options{Identify: func(_ context.Context, request mcp.Request) (*Identity, error) {
		if request == nil {
			t.Fatal("identify request is nil")
		}
		return &Identity{DistinctID: "real-user"}, nil
	}})
	events := make(chan *Event, 1)
	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "1"}, nil)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				attribution := analytics.resolveAttribution(request, "")
				events <- analytics.resolveIdentity(ctx, request, &attribution)
			}
			return next(ctx, method, request)
		}
	})
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(
		context.Context,
		*mcp.CallToolRequest,
		struct{},
	) (*mcp.CallToolResult, struct{}, error) {
		return nil, struct{}{}, nil
	})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	if _, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event == nil || event.DistinctID != "real-user" {
		t.Fatalf("identify event = %#v", event)
	}
	if event.Properties[PropertyResourceName] != "echo" {
		t.Errorf("identify resource = %#v", event.Properties[PropertyResourceName])
	}
}

type identityContextKey struct{}
