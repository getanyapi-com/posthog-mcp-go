package posthogmcp

// Portions of this file are derived from agentcathq/agentcat-typescript-sdk.
// Copyright (c) 2025 AgentCat, Inc. Licensed under the MIT License.

import (
	"fmt"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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
	sensitiveKey     = regexp.MustCompile(`(?i)^(authorization|cookie|set-cookie|x-api-key|api[-_]?key|api[-_]?token|access[-_]?token|refresh[-_]?token|token|password|secret|client[-_]?secret|private[-_]?key)$`)
	timeType         = reflect.TypeOf(time.Time{})
	errorType        = reflect.TypeOf((*error)(nil)).Elem()
)

type visitIdentity struct {
	kind reflect.Kind
	ptr  uintptr
}

func sanitizeValue(value any, depth int) any {
	return valueSanitizer{seen: make(map[visitIdentity]bool)}.visit(reflect.ValueOf(value), depth)
}

type valueSanitizer struct {
	seen map[visitIdentity]bool
}

func (s valueSanitizer) visit(value reflect.Value, depth int) any {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return s.visit(value.Elem(), depth)
	}
	if value.Type() == timeType {
		return value.Interface().(time.Time).Format(time.RFC3339Nano)
	}
	if value.Type().Implements(errorType) {
		return safeString(value.Interface())
	}
	switch value.Kind() {
	case reflect.Bool:
		return value.Interface()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Interface()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Interface()
	case reflect.Float32, reflect.Float64:
		if normalized := normalizeFloat(value.Float()); normalized != value.Float() {
			return normalized
		}
		return value.Interface()
	case reflect.String:
		return truncateUTF8(sanitizeString(value.String()), maxStringBytes)
	case reflect.Map:
		return s.visitMap(value, depth)
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return binaryRedaction
		}
		return s.visitSlice(value, depth)
	case reflect.Array:
		return s.visitArray(value, depth)
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		if depth <= 0 {
			return "[Object]"
		}
		return s.visitReference(value, depth, func() any { return s.visit(value.Elem(), depth-1) })
	case reflect.Struct:
		return s.visitStruct(value, depth)
	case reflect.Invalid:
		return nil
	default:
		if value.CanInterface() {
			return truncateUTF8(safeString(value.Interface()), maxStringBytes)
		}
		return "[unavailable]"
	}
}

func (s valueSanitizer) visitMap(value reflect.Value, depth int) any {
	if value.IsNil() {
		return nil
	}
	if depth <= 0 {
		return "[Object]"
	}
	return s.visitReference(value, depth, func() any {
		entries := make([]struct {
			key string
			val reflect.Value
		}, 0, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			entries = append(entries, struct {
				key string
				val reflect.Value
			}{safeString(iter.Key().Interface()), iter.Value()})
		}
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		result := make(map[string]any, min(len(entries), maxBreadth)+1)
		for index, entry := range entries {
			if index == maxBreadth {
				result["..."] = "[MaxProperties ~]"
				break
			}
			if sensitiveKey.MatchString(entry.key) {
				result[entry.key] = redactedValue
			} else {
				result[entry.key] = s.visit(entry.val, depth-1)
			}
		}
		return sanitizeResponseContent(result)
	})
}

func (s valueSanitizer) visitSlice(value reflect.Value, depth int) any {
	if value.IsNil() {
		return nil
	}
	if depth <= 0 {
		return "[Array]"
	}
	return s.visitReference(value, depth, func() any { return s.visitIndexed(value, depth) })
}

func (s valueSanitizer) visitArray(value reflect.Value, depth int) any {
	if depth <= 0 {
		return "[Array]"
	}
	return s.visitIndexed(value, depth)
}

func (s valueSanitizer) visitIndexed(value reflect.Value, depth int) any {
	limit := min(value.Len(), maxBreadth)
	result := make([]any, 0, limit+1)
	for index := 0; index < limit; index++ {
		result = append(result, s.visit(value.Index(index), depth-1))
	}
	if value.Len() > maxBreadth {
		result = append(result, "[MaxProperties ~]")
	}
	return result
}

func (s valueSanitizer) visitStruct(value reflect.Value, depth int) any {
	if depth <= 0 {
		return "[Object]"
	}
	result := make(map[string]any)
	typeOf := value.Type()
	for index := 0; index < value.NumField() && len(result) < maxBreadth; index++ {
		field := typeOf.Field(index)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if sensitiveKey.MatchString(name) {
			result[name] = redactedValue
		} else {
			result[name] = s.visit(value.Field(index), depth-1)
		}
	}
	return sanitizeResponseContent(result)
}

func (s valueSanitizer) visitReference(value reflect.Value, _ int, visit func() any) any {
	identity := visitIdentity{kind: value.Kind(), ptr: value.Pointer()}
	if s.seen[identity] {
		return "[Circular ~]"
	}
	s.seen[identity] = true
	defer delete(s.seen, identity)
	return visit()
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
