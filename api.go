// Package posthogmcp instruments servers built with the official MCP Go SDK
// and emits PostHog's canonical MCP analytics events.
package posthogmcp

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

// Identity is the resolved PostHog identity for one MCP caller.
type Identity struct {
	DistinctID string
	Properties posthog.Properties
	Groups     posthog.Groups
}

// Event is the mutable event shape supplied to BeforeSend.
type Event struct {
	UUID       string
	Event      string
	DistinctID string
	Timestamp  time.Time
	Properties posthog.Properties
	Groups     posthog.Groups
}

// IdentifyFunc resolves the identity associated with an MCP request.
type IdentifyFunc func(context.Context, mcp.Request) (*Identity, error)

// IntentFallbackFunc infers user intent when no analytics-owned context is supplied.
type IntentFallbackFunc func(context.Context, mcp.Request) (string, error)

// EventPropertiesFunc supplies properties attached to every automatic event.
type EventPropertiesFunc func(context.Context, mcp.Request) (posthog.Properties, error)

// BeforeSendFunc can modify or drop an event immediately before enqueue.
type BeforeSendFunc func(context.Context, Event) (*Event, error)

// Options configures MCP analytics behavior.
type Options struct {
	Logger                      *slog.Logger
	Identify                    IdentifyFunc
	IntentFallback              IntentFallbackFunc
	EventProperties             EventPropertiesFunc
	BeforeSend                  BeforeSendFunc
	DisableContextInjection     bool
	ContextDescription          string
	ReportMissing               bool
	MissingCapabilityToolName   string
	EnableConversationID        bool
	DisableExceptionAutocapture bool
}

// Analytics is a concurrency-safe MCP analytics middleware and custom capture handle.
type Analytics struct {
	client          posthog.EnqueueClient
	options         Options
	fallbackSession string
	mu              sync.RWMutex
}

// New constructs analytics. A nil client produces disabled pass-through instrumentation.
func New(client posthog.EnqueueClient, options *Options) *Analytics {
	var configured Options
	if options != nil {
		configured = *options
	}
	if configured.MissingCapabilityToolName == "" {
		configured.MissingCapabilityToolName = DefaultMissingCapabilityToolName
	}
	return &Analytics{
		client:          client,
		options:         configured,
		fallbackSession: newPrefixedID("ses"),
	}
}

// Enabled reports whether this object has a PostHog enqueue client.
func (a *Analytics) Enabled() bool {
	return a != nil && a.client != nil
}
