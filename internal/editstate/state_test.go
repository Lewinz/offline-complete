package editstate

import (
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestCloseInvalidatesSnapshotAndRejectsUpdates(t *testing.T) {
	for _, version := range []uint64{1, ^uint64(0)} {
		var state State
		if err := state.Update(version, "private", 7); err != nil {
			t.Fatal(err)
		}
		state.Close()
		state.Close()
		if got, ok := state.Snapshot(); ok || got != (Snapshot{}) {
			t.Fatalf("closed state retained snapshot: %+v, %v", got, ok)
		}
		if err := state.Update(^uint64(0), "late", 4); err == nil {
			t.Fatal("closed state accepted an update")
		}
		if err := state.Clear(^uint64(0)); err == nil {
			t.Fatal("closed state accepted a clear")
		}
	}
}

func TestCloseConcurrentWithUpdates(t *testing.T) {
	var state State
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for version := uint64(1); version <= 1000; version++ {
				_ = state.Update(version, "input", 5)
			}
		}()
	}
	state.Close()
	wg.Wait()
	if got, ok := state.Snapshot(); ok || got != (Snapshot{}) {
		t.Fatalf("closed state restored a snapshot: %+v, %v", got, ok)
	}
}

func TestSnapshotDoesNotRetainLargerInputBuffer(t *testing.T) {
	large := strings.Repeat("s", 4*1024*1024)
	line := large[1024:1032]
	var state State
	if err := state.Update(1, line, len(line)); err != nil {
		t.Fatal(err)
	}
	got, ok := state.Snapshot()
	if !ok || got.Line != "ssssssss" {
		t.Fatalf("unexpected snapshot: %+v, %v", got, ok)
	}
	if unsafe.StringData(got.Line) == unsafe.StringData(line) {
		t.Fatal("snapshot retains caller's larger input buffer")
	}
}

func TestSnapshotsAndInvalidation(t *testing.T) {
	var state State
	if _, ok := state.Snapshot(); ok {
		t.Fatal("initial state is present")
	}
	if err := state.Update(1, "git café", 9); err != nil {
		t.Fatal(err)
	}
	want := Snapshot{Version: 1, Line: "git café", Cursor: 9}
	if got, ok := state.Snapshot(); !ok || got != want {
		t.Fatalf("snapshot = %+v, %v", got, ok)
	}
	if err := state.Update(2, "git café", 4); err != nil {
		t.Fatal(err)
	}
	if got, ok := state.Snapshot(); !ok || got.Cursor != 4 || got.Version != 2 {
		t.Fatalf("moved snapshot = %+v, %v", got, ok)
	}
	if err := state.Clear(3); err != nil {
		t.Fatal(err)
	}
	if got, ok := state.Snapshot(); ok || got != (Snapshot{}) {
		t.Fatalf("cleared snapshot = %+v, %v", got, ok)
	}
	if err := state.Update(2, "stale secret", 0); err == nil {
		t.Fatal("accepted stale update after clear")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("stale update restored cleared input")
	}
	if err := state.Update(4, "", 0); err != nil {
		t.Fatal(err)
	}
	if got, ok := state.Snapshot(); !ok || got != (Snapshot{Version: 4}) {
		t.Fatalf("empty snapshot = %+v, %v", got, ok)
	}
}

func TestInvalidUpdatePreservesSnapshot(t *testing.T) {
	var state State
	if err := state.Update(5, "café", 5); err != nil {
		t.Fatal(err)
	}
	for _, version := range []uint64{0, 5} {
		if err := state.Update(version, "", 0); err == nil {
			t.Errorf("accepted stale version %d", version)
		}
		if got, ok := state.Snapshot(); !ok || got != (Snapshot{Version: 5, Line: "café", Cursor: 5}) {
			t.Fatalf("stale update changed snapshot: %+v, %v", got, ok)
		}
	}
	if err := state.Clear(5); err == nil {
		t.Fatal("accepted stale clear")
	}
	if _, ok := state.Snapshot(); !ok {
		t.Fatal("stale clear removed snapshot")
	}
	if err := state.Update(6, strings.Repeat("x", 65536), 65536); err != nil {
		t.Fatalf("boundary: %v", err)
	}
	for _, tc := range []struct {
		line   string
		cursor int
	}{
		{"\xff", 0}, {"café", 4}, {"abc", -1}, {"abc", 4}, {strings.Repeat("x", 65537), 0},
	} {
		var s State
		if err := s.Update(1, "previous", 8); err != nil {
			t.Fatal(err)
		}
		if err := s.Update(2, tc.line, tc.cursor); err == nil {
			t.Errorf("accepted invalid update: length=%d cursor=%d", len(tc.line), tc.cursor)
		}
		if _, ok := s.Snapshot(); ok {
			t.Fatal("invalid new update retained stale snapshot")
		}
	}
}

func TestNewerInvalidUpdateInvalidatesOldSnapshot(t *testing.T) {
	var state State
	if err := state.Update(1, "old input", 9); err != nil {
		t.Fatal(err)
	}
	if err := state.Update(3, "café", 4); err == nil {
		t.Fatal("accepted invalid cursor")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("invalid newer update left stale snapshot visible")
	}
	if err := state.Update(2, "old input", 9); err == nil {
		t.Fatal("accepted update older than rejected state")
	}
	if err := state.Update(4, "new", 3); err != nil {
		t.Fatal(err)
	}
}

func TestInstancesAndConcurrentSnapshots(t *testing.T) {
	var a, b State
	if err := a.Update(1, "private", 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Snapshot(); ok {
		t.Fatal("state leaked across instances")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				a.Snapshot()
			}
		}()
	}
	for i := uint64(2); i <= 100; i++ {
		if err := a.Update(i, "next", 4); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if got, ok := a.Snapshot(); !ok || got != (Snapshot{Version: 100, Line: "next", Cursor: 4}) {
		t.Fatalf("final snapshot = %+v, %v", got, ok)
	}
}
