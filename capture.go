package posthogmcp

import (
	"context"
	"fmt"
	"time"

	"github.com/posthog/posthog-go"
)

// Capture records a custom event. Disabled analytics treats capture as a no-op.
func (a *Analytics) Capture(ctx context.Context, name string, properties map[string]any) error {
	if !a.Enabled() {
		return nil
	}
	if name == "" {
		return fmt.Errorf("posthogmcp: capture requires an event name")
	}
	props := cloneProperties(properties)
	props[PropertySource] = Source
	props["$mcp_sdk_language"] = SDKLanguage
	props["$mcp_sdk_version"] = Version
	props[PropertySessionID] = a.fallbackSession
	props["$process_person_profile"] = false

	event := Event{
		UUID:       newPrefixedID("evt")[4:],
		Event:      name,
		DistinctID: a.fallbackSession,
		Timestamp:  time.Now(),
		Properties: props,
	}
	return a.enqueue(ctx, event)
}

func (a *Analytics) enqueue(ctx context.Context, event Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("posthogmcp: enqueue panic: %v", recovered)
		}
	}()
	return a.client.Enqueue(posthog.Capture{
		Uuid:       event.UUID,
		DistinctId: event.DistinctID,
		Event:      event.Event,
		Timestamp:  event.Timestamp,
		Properties: event.Properties,
		Groups:     event.Groups,
	})
}

func cloneProperties(source map[string]any) posthog.Properties {
	cloned := make(posthog.Properties, len(source)+5)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
