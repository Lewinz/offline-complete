//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentAutojumpSelectionDoesNotReoffer(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("..", "..", "rules", "base.json"))
	if err != nil {
		t.Fatal(err)
	}
	specs, err := filepath.Abs(filepath.Join("..", "..", "rules", "specs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OFFLINE_COMPLETE_SPECS", specs)
	a := startTestAgent(t, string(base))

	autojump := filepath.Join(os.Getenv("HOME"), "Library", "autojump")
	if err := os.MkdirAll(autojump, 0o700); err != nil {
		t.Fatal(err)
	}
	entries := "10\t/Users/test/sub2api/backend/cmd\n20\t/Users/test/sub2api/backend/gateway\n30\t/Users/test/sub2api/backend/server\n40\t/Users/test/sub2api\n"
	if err := os.WriteFile(filepath.Join(autojump, "autojump.txt"), []byte(entries), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := a.stdin.Write([]byte("j sub2api")); err != nil {
		t.Fatal(err)
	}
	snapshot := a.waitState(t, "j sub2api", 9)
	var offer agentMessage
	deadline := time.Now().Add(10 * time.Second)
	for {
		message := readAgentMessage(t, a.connection, a.reader, time.Until(deadline))
		if message.Type == "offer" && message.Version == snapshot.Version {
			for _, item := range message.Items {
				if item.Name == "sub2api" {
					offer = message
				}
			}
			if offer.Type == "offer" {
				break
			}
		}
	}
	index := -1
	for i, item := range offer.Items {
		if item.Name == "sub2api" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("sub2api index missing: %+v", offer.Items)
	}
	if _, err := fmt.Fprintf(a.connection, `{"type":"select","version":%d,"offer":%d,"index":%d}`+"\n", offer.Version, offer.Offer, index); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		snapshot, present := a.runtime.state.Snapshot()
		if present && len(snapshot.Line) > len("j sub2api") && snapshot.Line[:len("j sub2api")] == "j sub2api" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("selected line not applied: %+v present=%v", snapshot, present)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		message := readAgentMessage(t, a.connection, a.reader, 5*time.Second)
		if message.Type == "result" {
			break
		}
	}

	_ = a.connection.SetReadDeadline(time.Now().Add(750 * time.Millisecond))
	for {
		line, err := a.reader.ReadString('\n')
		if err != nil {
			break
		}
		var message agentMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "offer" && len(message.Items) > 0 {
			t.Fatalf("re-offered after autojump selection: %+v", message)
		}
	}
	_ = a.connection.SetReadDeadline(time.Now().Add(5 * time.Second))

	outcome := a.stop(t)
	if outcome.err != nil || outcome.code != 0 {
		t.Fatalf("agent exit: %+v; output %q", outcome, a.output.String())
	}
}
