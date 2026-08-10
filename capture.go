package posthogmcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

// Capture records a custom event. Disabled analytics treats capture as a no-op.
func (a *Analytics) Capture(ctx context.Context, name string, properties map[string]any) (err error) {
	if !a.Enabled() {
		return nil
	}
	if name == "" {
		return fmt.Errorf("posthogmcp: capture requires an event name")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.logFailure(ctx, "capture pipeline panic", recovered)
			err = nil
		}
	}()
	a.captureEvent(ctx, Event{
		UUID:       eventUUID(),
		Event:      name,
		DistinctID: a.fallbackSession,
		Timestamp:  time.Now(),
		Properties: cloneProperties(properties),
	}, nil)
	return nil
}

// captureEvent is the common fail-open delivery seam for custom and automatic events.
func (a *Analytics) captureEvent(ctx context.Context, input Event, request mcp.Request) {
	if !a.Enabled() {
		return
	}
	event := input
	if event.UUID == "" {
		event.UUID = eventUUID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.DistinctID == "" {
		event.DistinctID = a.fallbackSession
	}
	properties := make(posthog.Properties)
	for key, value := range a.resolveEventProperties(ctx, request) {
		properties[key] = value
	}
	for key, value := range event.Properties {
		properties[key] = value
	}
	properties[PropertySource] = Source
	event.Properties = properties
	if attribution, ok := requestAttributionFromContext(ctx); ok {
		attribution.applyToEvent(&event)
		properties = event.Properties
	}
	if _, exists := properties[PropertySessionID]; !exists && a.fallbackSession != "" {
		properties[PropertySessionID] = a.fallbackSession
	}
	if _, identified := properties["$set"]; !identified && event.DistinctID == properties[PropertySessionID] {
		properties["$process_person_profile"] = false
	}
	event = sanitizeAndBoundEvent(event)

	processed, keep := a.applyBeforeSend(ctx, event)
	if !keep {
		return
	}
	// A hook owns its returned value. Re-snapshot it before handing mutable data
	// to PostHog, and retain the package privacy and size guarantees.
	processed = sanitizeAndBoundEvent(processed)
	if eventJSONSize(processed) > maxEventBytes {
		a.logFailure(ctx, "event exceeds size budget", maxEventBytes)
		return
	}
	a.safeEnqueue(ctx, toPostHogCapture(processed))
}

func (a *Analytics) resolveEventProperties(ctx context.Context, request mcp.Request) (properties posthog.Properties) {
	if a.options.EventProperties == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.logFailure(ctx, "event properties panic", recovered)
			properties = nil
		}
	}()
	resolved, err := a.options.EventProperties(ctx, request)
	if err != nil {
		a.logFailure(ctx, "event properties error", err)
		return nil
	}
	return resolved
}

func (a *Analytics) applyBeforeSend(ctx context.Context, input Event) (event Event, keep bool) {
	if a.options.BeforeSend == nil {
		return input, true
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.logFailure(ctx, "before send panic", recovered)
			keep = false
		}
	}()
	processed, err := a.options.BeforeSend(ctx, input)
	if err != nil {
		a.logFailure(ctx, "before send error", err)
		return Event{}, false
	}
	if processed == nil {
		return Event{}, false
	}
	return *processed, true
}

func (a *Analytics) safeEnqueue(ctx context.Context, capture posthog.Capture) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.logFailure(ctx, "enqueue panic", recovered)
		}
	}()
	if err := a.client.Enqueue(capture); err != nil {
		a.logFailure(ctx, "enqueue error", err)
	}
}

func (a *Analytics) logFailure(ctx context.Context, message string, cause any) {
	if a == nil || a.options.Logger == nil {
		return
	}
	defer func() { _ = recover() }()
	if ctx == nil {
		ctx = context.Background()
	}
	a.options.Logger.ErrorContext(ctx, message, "cause", safeString(cause))
}

func eventUUID() string {
	return newPrefixedID("evt")[4:]
}

func cloneProperties(source map[string]any) posthog.Properties {
	cloned := make(posthog.Properties, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
