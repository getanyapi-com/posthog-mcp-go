package posthogmcp

// Portions of this file are derived from agentcathq/agentcat-typescript-sdk.
// Copyright (c) 2025 AgentCat, Inc. Licensed under the MIT License.

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/posthog/posthog-go"
)

const maxEventBytes = 102_400

type valuePath []string

type stringLocation struct {
	path   valuePath
	length int
}

func sanitizeAndBoundEvent(event Event) Event {
	result := event
	result.Event = truncateUTF8(sanitizeString(event.Event), maxStringBytes)
	result.DistinctID = truncateUTF8(sanitizeString(event.DistinctID), maxStringBytes)
	result.Properties = normalizeProperties(event.Properties, maxDepth)
	result.Groups = normalizeGroups(event.Groups)
	return truncateEventToSize(result)
}

func normalizeProperties(properties map[string]any, depth int) posthog.Properties {
	if properties == nil {
		return posthog.Properties{}
	}
	normalized, ok := sanitizeValue(properties, depth).(map[string]any)
	if !ok {
		return posthog.Properties{}
	}
	result := posthog.Properties(normalized)
	applyFieldLimits(result)
	return result
}

func applyFieldLimits(properties posthog.Properties) {
	truncateProperty(properties, PropertyIntent, 2_048)
	truncateProperty(properties, PropertyErrorMessage, 2_048)
	truncateProperty(properties, PropertyResourceName, 256)
	for _, key := range []string{
		PropertyServerName,
		PropertyServerVersion,
		PropertyClientName,
		PropertyClientVersion,
		PropertyClientUserAgent,
		PropertyVendorClient,
		PropertyErrorType,
	} {
		truncateProperty(properties, key, 256)
	}
	truncateExceptions(properties)
}

func truncateProperty(properties posthog.Properties, key string, maxBytes int) {
	if value, ok := properties[key].(string); ok {
		properties[key] = truncateUTF8(value, maxBytes)
	}
}

func truncateExceptions(properties posthog.Properties) {
	exceptions, ok := properties["$exception_list"].([]any)
	if !ok {
		return
	}
	for _, item := range exceptions {
		exception, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if message, ok := exception["value"].(string); ok {
			exception["value"] = truncateUTF8(message, 2_048)
		}
		stacktrace, ok := exception["stacktrace"].(map[string]any)
		if !ok {
			continue
		}
		frames, ok := stacktrace["frames"].([]any)
		if !ok || len(frames) <= 50 {
			continue
		}
		bounded := make([]any, 0, 50)
		bounded = append(bounded, frames[:25]...)
		bounded = append(bounded, frames[len(frames)-25:]...)
		stacktrace["frames"] = bounded
	}
}

func normalizeGroups(groups posthog.Groups) posthog.Groups {
	if groups == nil {
		return nil
	}
	normalized, ok := sanitizeValue(groups, maxDepth).(map[string]any)
	if !ok {
		return nil
	}
	return posthog.Groups(normalized)
}

func truncateEventToSize(event Event) Event {
	if eventJSONSize(event) <= maxEventBytes {
		return event
	}
	for depth := maxDepth - 1; depth >= 1; depth-- {
		reduced := event
		reduced.Properties = normalizeProperties(event.Properties, depth)
		if eventJSONSize(reduced) <= maxEventBytes {
			return reduced
		}
	}
	minimal := event
	minimal.Properties = normalizeProperties(event.Properties, 1)
	return truncateLargestEventStrings(minimal)
}

func truncateLargestEventStrings(event Event) Event {
	result := event
	result.Properties = cloneNormalizedProperties(event.Properties)
	for attempt := 0; attempt < 10; attempt++ {
		currentSize := eventJSONSize(result)
		if currentSize <= maxEventBytes {
			return result
		}
		locations := collectStrings(result.Properties)
		if len(result.Event) > 100 {
			locations = append(locations, stringLocation{path: valuePath{"$event"}, length: len(result.Event)})
		}
		sort.SliceStable(locations, func(i, j int) bool {
			if locations[i].length == locations[j].length {
				return strings.Join(locations[i].path, "\x00") < strings.Join(locations[j].path, "\x00")
			}
			return locations[i].length > locations[j].length
		})
		remaining := currentSize - maxEventBytes + 200
		changed := false
		for _, location := range locations {
			if remaining <= 0 {
				break
			}
			reduction := min(remaining, location.length/2)
			if reduction < 10 {
				continue
			}
			if len(location.path) == 1 && location.path[0] == "$event" {
				result.Event = truncateUTF8(result.Event, location.length-reduction)
			} else if truncateAtPath(result.Properties, location.path, location.length-reduction) {
				changed = true
			}
			remaining -= reduction
			changed = true
		}
		if !changed {
			return result
		}
	}
	return result
}

func eventJSONSize(event Event) int {
	wire, err := json.Marshal(toPostHogCapture(event))
	if err != nil {
		return maxEventBytes + 1
	}
	return len(wire)
}

func collectStrings(value any) []stringLocation {
	var locations []stringLocation
	collectStringLocations(value, nil, &locations)
	return locations
}

func collectStringLocations(value any, path valuePath, locations *[]stringLocation) {
	switch typed := value.(type) {
	case string:
		if len(typed) > 100 {
			copied := append(valuePath(nil), path...)
			*locations = append(*locations, stringLocation{path: copied, length: len(typed)})
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectStringLocations(typed[key], append(path, key), locations)
		}
	case posthog.Properties:
		collectStringLocations(map[string]any(typed), path, locations)
	case []any:
		for index, nested := range typed {
			collectStringLocations(nested, append(path, strconv.Itoa(index)), locations)
		}
	}
}

func truncateAtPath(properties posthog.Properties, path valuePath, maxBytes int) bool {
	if len(path) == 0 {
		return false
	}
	var current any = map[string]any(properties)
	for _, part := range path[:len(path)-1] {
		switch typed := current.(type) {
		case map[string]any:
			current = typed[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	last := path[len(path)-1]
	switch typed := current.(type) {
	case map[string]any:
		value, ok := typed[last].(string)
		if !ok {
			return false
		}
		typed[last] = truncateUTF8(value, maxBytes)
		return true
	case []any:
		index, err := strconv.Atoi(last)
		if err != nil || index < 0 || index >= len(typed) {
			return false
		}
		value, ok := typed[index].(string)
		if !ok {
			return false
		}
		typed[index] = truncateUTF8(value, maxBytes)
		return true
	default:
		return false
	}
}

func cloneNormalizedProperties(properties posthog.Properties) posthog.Properties {
	cloned, ok := sanitizeValue(properties, maxDepth).(map[string]any)
	if !ok {
		return posthog.Properties{}
	}
	return posthog.Properties(cloned)
}

func toPostHogCapture(event Event) posthog.Capture {
	return posthog.Capture{
		Uuid:       event.UUID,
		DistinctId: event.DistinctID,
		Event:      event.Event,
		Timestamp:  event.Timestamp,
		Properties: event.Properties,
		Groups:     event.Groups,
	}
}
