package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"offline-complete/internal/store"
)

func TestRecordLookupPersistAndPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := first.Record("git\x00\x00sub\x000\x00commit", now); err != nil {
		t.Fatal(err)
	}
	if err := first.Record("git\x00\x00sub\x000\x00commit", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	selection, ok := first.Lookup("git\x00\x00sub\x000\x00commit")
	if !ok || selection.Count != 2 {
		t.Fatalf("selection = %+v ok=%v", selection, ok)
	}

	info, err := os.Stat(filepath.Join(dir, "learning.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("data file mode = %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("data dir mode = %o", dirInfo.Mode().Perm())
	}

	second, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	selection, ok = second.Lookup("git\x00\x00sub\x000\x00commit")
	if !ok || selection.Count != 2 {
		t.Fatalf("reloaded selection = %+v ok=%v", selection, ok)
	}
}

func TestDisableAndClear(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.SetLearning(false); err != nil {
		t.Fatal(err)
	}
	if err := data.Record("key", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := data.Lookup("key"); ok {
		t.Fatal("disabled store returned a selection")
	}
	if err := data.SetLearning(true); err != nil {
		t.Fatal(err)
	}
	if err := data.Record("key", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := data.Lookup("key"); !ok {
		t.Fatal("enabled store lost the selection")
	}
	if err := data.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok := data.Lookup("key"); ok {
		t.Fatal("clear kept a selection")
	}
}

func TestPrunesExpiredSelections(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-store.Retention - 24*time.Hour)
	data, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.Record("old", old); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Lookup("old"); ok {
		t.Fatal("expired selection was kept")
	}
}

func TestRejectsNonPrivateDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(dir); err == nil {
		t.Fatal("accepted a non-private data directory")
	}
}

func TestExternalToggleAndClearAreVisible(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	agentStore, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	external, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "git\x00\x00opt\x000\x00--all"
	if err := external.SetLearning(false); err != nil {
		t.Fatal(err)
	}
	if agentStore.Learning() {
		t.Fatal("agent did not observe external disable")
	}
	if err := agentStore.Record(key, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := agentStore.Lookup(key); ok {
		t.Fatal("disabled agent recorded a selection")
	}
	if err := external.SetLearning(true); err != nil {
		t.Fatal(err)
	}
	if err := external.Record(key, time.Now()); err != nil {
		t.Fatal(err)
	}
	selection, ok := agentStore.Lookup(key)
	if !ok || selection.Count != 1 {
		t.Fatalf("agent did not observe external record: %+v ok=%v", selection, ok)
	}
	if err := external.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, ok := agentStore.Lookup(key); ok {
		t.Fatal("agent did not observe external clear")
	}
}
