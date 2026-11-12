package ipc_test

import (
	"regexp"
	"testing"

	"offline-complete/internal/ipc"
)

func TestNewSessionIDGeneratesDistinct128BitHexIDs(t *testing.T) {
	seen := make(map[string]bool)
	pattern := regexp.MustCompile(`^[0-9a-f]{32}$`)
	for i := 0; i < 256; i++ {
		id, err := ipc.NewSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(id) || seen[id] {
			t.Fatalf("invalid or duplicate session ID %q", id)
		}
		seen[id] = true
	}
}
