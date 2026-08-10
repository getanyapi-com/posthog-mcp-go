package posthogmcp

import "github.com/google/uuid"

func newPrefixedID(prefix string) string {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	return prefix + "_" + id.String()
}
