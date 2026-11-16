package agent

import "testing"

func TestParseRefreshCommand(t *testing.T) {
	parsed, err := parseCommand([]byte(`{"type":"refresh"}`))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.kind != commandRefresh {
		t.Fatalf("kind = %v", parsed.kind)
	}
}

func TestParseRefreshCommandRejectsUnknownFields(t *testing.T) {
	if _, err := parseCommand([]byte(`{"type":"refresh","command":"dotfiles"}`)); err == nil {
		t.Fatal("accepted unknown refresh field")
	}
}
