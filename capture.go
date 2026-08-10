package posthogmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

type resolvedEventProperties struct {
	properties posthog.Properties
}

type resolvedEventPropertiesKey struct{}

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
	sessionID := a.generatedSessionID(nil)
	a.captureEvent(ctx, Event{
		UUID:       eventUUID(),
		Event:      name,
		DistinctID: sessionID,
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
	fallbackSession := a.fallbackSessionSnapshot()
	if event.UUID == "" {
		event.UUID = eventUUID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.DistinctID == "" {
		event.DistinctID = fallbackSession
	}
	properties := make(posthog.Properties)
	resolved, ok := eventPropertiesFromContext(ctx)
	if !ok {
		resolved = a.resolveEventProperties(ctx, request)
	}
	for key, value := range resolved {
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
	if _, exists := properties[PropertySessionID]; !exists && event.DistinctID != "" {
		properties[PropertySessionID] = event.DistinctID
	}
	if _, identified := properties["$set"]; !identified && event.DistinctID == properties[PropertySessionID] {
		properties["$process_person_profile"] = false
	}
	event = sanitizeAndBoundEvent(event)
	if eventJSONSize(event) > maxEventBytes {
		a.logFailure(ctx, "event exceeds size budget", maxEventBytes)
		return
	}

	processed, keep := a.applyBeforeSend(ctx, event)
	if !keep {
		return
	}
	processed, ok = snapshotHookEvent(processed)
	if !ok {
		a.logFailure(ctx, "before send returned an unserializable event", nil)
		return
	}
	a.safeEnqueue(ctx, toPostHogCapture(processed))
}

func withResolvedEventProperties(ctx context.Context, properties posthog.Properties) context.Context {
	return context.WithValue(ctx, resolvedEventPropertiesKey{}, resolvedEventProperties{properties: properties})
}

func eventPropertiesFromContext(ctx context.Context) (posthog.Properties, bool) {
	if ctx == nil {
		return nil, false
	}
	resolved, ok := ctx.Value(resolvedEventPropertiesKey{}).(resolvedEventProperties)
	return resolved.properties, ok
}

func snapshotHookEvent(event Event) (snapshot Event, ok bool) {
	snapshot = event
	snapshot.Properties = cloneHookProperties(event.Properties)
	snapshot.Groups = cloneHookGroups(event.Groups)
	return snapshot, true
}

func cloneHookProperties(source posthog.Properties) posthog.Properties {
	if source == nil {
		return nil
	}
	clone := make(posthog.Properties, len(source))
	for key, value := range source {
		clone[key] = cloneHookValue(value)
	}
	return clone
}

func cloneHookGroups(source posthog.Groups) posthog.Groups {
	if source == nil {
		return nil
	}
	clone := make(posthog.Groups, len(source))
	for key, value := range source {
		clone[key] = cloneHookValue(value)
	}
	return clone
}

func cloneHookValue(value any) any {
	switch value := value.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number, time.Time:
		return value
	case map[string]any:
		clone := make(map[string]any, len(value))
		for key, nested := range value {
			clone[key] = cloneHookValue(nested)
		}
		return clone
	case []any:
		clone := make([]any, len(value))
		for index, nested := range value {
			clone[index] = cloneHookValue(nested)
		}
		return clone
	case []string:
		return append([]string(nil), value...)
	default:
		return cloneHookJSONValue(value)
	}
}

func cloneHookJSONValue(value any) (cloned any) {
	defer func() {
		if recover() != nil {
			cloned = "[unavailable]"
		}
	}()
	wire, err := json.Marshal(value)
	if err != nil {
		return "[unavailable]"
	}
	decoder := json.NewDecoder(strings.NewReader(string(wire)))
	decoder.UseNumber()
	if decoder.Decode(&cloned) != nil {
		return "[unavailable]"
	}
	return cloned
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
	resolved, err := a.options.EventProperties(ctx, cloneRequestForCallback(request))
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
