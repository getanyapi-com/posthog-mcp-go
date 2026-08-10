package posthogmcp

// Portions of this file are derived from agentcathq/agentcat-typescript-sdk.
// Copyright (c) 2025 AgentCat, Inc. Licensed under the MIT License.

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/posthog/posthog-go"
)

const (
	maxDepth        = 10
	maxBreadth      = 100
	maxStringBytes  = 32_768
	base64SizeGate  = 10_240
	redactedValue   = "[redacted]"
	binaryRedaction = "[binary data redacted - not supported by PostHog MCP analytics]"
)

var (
	base64Pattern    = regexp.MustCompile(`^[A-Za-z0-9+/\n\r]+=*$`)
	base64URLPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+={0,2}$`)
	dataURLPattern   = regexp.MustCompile(`(?i)^data:[^,\s]*;base64,`)
	dataPayload      = regexp.MustCompile(`^[A-Za-z0-9+/_-]+={0,2}$`)
	posthogToken     = regexp.MustCompile(`\bph[a-z]_[A-Za-z0-9_-]{20,}\b`)
	sensitiveKey     = regexp.MustCompile(`(?i)^(authorization|cookie|set-cookie|x-api-key|api[-_]?key|api[-_]?token|access[-_]?token|refresh[-_]?token|token|password|secret|credentials?|client[-_]?secret|private[-_]?key)$`)
)

type valueSanitizer struct {
	seen map[string]bool
}

func sanitizeValue(value any, depth int) any {
	return valueSanitizer{seen: make(map[string]bool)}.visit(value, depth)
}

func (sanitizer valueSanitizer) visit(value any, depth int) any {
	switch value := value.(type) {
	case nil:
		return nil
	case bool:
		return value
	case string:
		return truncateUTF8(sanitizeString(value), maxStringBytes)
	case time.Time:
		return value.Format(time.RFC3339Nano)
	case error:
		return truncateUTF8(safeString(value), maxStringBytes)
	case []byte:
		return binaryRedaction
	case float64:
		return normalizeFloat(value)
	case float32:
		return normalizeFloat(float64(value))
	case int, int8, int16, int32, int64:
		return value
	case uint, uint8, uint16, uint32, uint64:
		return value
	case json.Number:
		return value
	case map[string]any:
		return sanitizer.visitMap(value, depth)
	case posthog.Properties:
		return sanitizer.visitMap(map[string]any(value), depth)
	case posthog.Groups:
		return sanitizer.visitMap(map[string]any(value), depth)
	case []any:
		return sanitizer.visitSlice(value, depth)
	case []string:
		items := make([]any, len(value))
		for index := range value {
			items[index] = value[index]
		}
		return sanitizer.visitSlice(items, depth)
	default:
		return sanitizer.visitJSONValue(value, depth)
	}
}

func (sanitizer valueSanitizer) visitMap(value map[string]any, depth int) any {
	if value == nil {
		return nil
	}
	if depth <= 0 {
		return "[Object]"
	}
	key := referenceKey("map", value)
	return sanitizer.visitReference(key, func() any {
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make(map[string]any, min(len(keys), maxBreadth)+1)
		for index, key := range keys {
			if index == maxBreadth {
				result["..."] = "[MaxProperties ~]"
				break
			}
			if sensitiveKey.MatchString(key) {
				result[key] = redactedValue
			} else {
				result[key] = sanitizer.visit(value[key], depth-1)
			}
		}
		return sanitizeResponseContent(result)
	})
}

func (sanitizer valueSanitizer) visitSlice(value []any, depth int) any {
	if value == nil {
		return nil
	}
	if depth <= 0 {
		return "[Array]"
	}
	key := referenceKey("slice", value)
	return sanitizer.visitReference(key, func() any {
		limit := min(len(value), maxBreadth)
		result := make([]any, 0, limit+1)
		for index := 0; index < limit; index++ {
			result = append(result, sanitizer.visit(value[index], depth-1))
		}
		if len(value) > maxBreadth {
			result = append(result, "[MaxProperties ~]")
		}
		return result
	})
}

func (sanitizer valueSanitizer) visitJSONValue(value any, depth int) (result any) {
	defer func() {
		if recover() != nil {
			result = "[unavailable]"
		}
	}()
	wire, err := json.Marshal(value)
	if err != nil {
		return truncateUTF8(sanitizeString(safeString(value)), maxStringBytes)
	}
	var normalized any
	decoder := json.NewDecoder(strings.NewReader(string(wire)))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return "[unavailable]"
	}
	return sanitizer.visit(normalized, depth)
}

func (sanitizer valueSanitizer) visitReference(key string, visit func() any) any {
	if sanitizer.seen[key] {
		return "[Circular ~]"
	}
	sanitizer.seen[key] = true
	defer delete(sanitizer.seen, key)
	return visit()
}

func referenceKey(kind string, value any) string {
	return fmt.Sprintf("%s:%p", kind, value)
}

func normalizeFloat(value float64) any {
	switch {
	case math.IsNaN(value):
		return "[NaN]"
	case math.IsInf(value, 1):
		return "[Infinity]"
	case math.IsInf(value, -1):
		return "[-Infinity]"
	default:
		return value
	}
}

func sanitizeString(value string) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) >= base64SizeGate && isEncodedBinary(value) {
		return binaryRedaction
	}
	return posthogToken.ReplaceAllString(value, redactedValue)
}

func isEncodedBinary(value string) bool {
	if base64Pattern.MatchString(value) || (strings.ContainsAny(value, "-_") && base64URLPattern.MatchString(value)) {
		return true
	}
	prefix := dataURLPattern.FindString(value)
	if prefix == "" {
		return false
	}
	payload, err := url.PathUnescape(value[len(prefix):])
	return err == nil && dataPayload.MatchString(strings.NewReplacer("\r", "", "\n", "").Replace(payload))
}

func sanitizeContentBlock(block map[string]any) any {
	typeName, hasType := block["type"].(string)
	if !hasType {
		return block
	}
	switch typeName {
	case "text", "resource_link":
		return block
	case "image", "audio":
		return map[string]any{"type": "text", "text": "[" + typeName + " content redacted - not supported by PostHog MCP analytics]"}
	case "resource":
		if resource, ok := block["resource"].(map[string]any); ok {
			if _, blob := resource["blob"]; blob {
				return map[string]any{"type": "text", "text": "[binary resource content redacted - not supported by PostHog MCP analytics]"}
			}
		}
		return block
	default:
		return map[string]any{"type": "text", "text": fmt.Sprintf("[unsupported content type %q redacted - not supported by PostHog MCP analytics]", typeName)}
	}
}

func sanitizeResponseContent(value map[string]any) map[string]any {
	content, ok := value["content"].([]any)
	if !ok {
		return value
	}
	blocks := make([]any, len(content))
	for index, item := range content {
		if block, isBlock := item.(map[string]any); isBlock {
			blocks[index] = sanitizeContentBlock(block)
		} else {
			blocks[index] = item
		}
	}
	value["content"] = blocks
	return value
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "..."
}

func safeString(value any) (result string) {
	defer func() {
		if recover() != nil {
			result = "[unavailable]"
		}
	}()
	return fmt.Sprint(value)
}
