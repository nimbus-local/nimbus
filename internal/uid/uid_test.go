package uid

import (
	"strings"
	"testing"
)

func TestNew_Format(t *testing.T) {
	id := New()

	// UUID v4 format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("expected 5 parts separated by '-', got %d: %s", len(parts), id)
	}

	lengths := []int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != lengths[i] {
			t.Errorf("part %d: expected length %d, got %d (%s)", i, lengths[i], len(part), part)
		}
	}
}

func TestNew_Version4(t *testing.T) {
	id := New()
	// 3rd group must start with '4' (version 4)
	parts := strings.Split(id, "-")
	if parts[2][0] != '4' {
		t.Errorf("expected version 4 UUID, got version byte %c in %s", parts[2][0], id)
	}
}

func TestNew_Variant(t *testing.T) {
	id := New()
	// 4th group first char must be 8, 9, a, or b (RFC 4122 variant)
	parts := strings.Split(id, "-")
	c := parts[3][0]
	if c != '8' && c != '9' && c != 'a' && c != 'b' {
		t.Errorf("expected variant bits (8/9/a/b), got %c in %s", c, id)
	}
}

func TestNew_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := New()
		if seen[id] {
			t.Fatalf("duplicate UUID generated: %s", id)
		}
		seen[id] = true
	}
}
