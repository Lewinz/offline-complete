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

func TestCorrectionNeedsRepeatedEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	contextKey := "cd\x00\x00pos\x000"
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if err := data.RecordCorrection(contextKey, "Downloads/", "Documents/", at); err != nil {
		t.Fatal(err)
	}
	if got := data.PersonalCandidates(contextKey, "Do", 10); len(got) != 0 {
		t.Fatalf("one correction became a personal candidate: %+v", got)
	}
	if err := data.RecordCorrection(contextKey, "Downloads/", "Documents/", at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := data.RecordCorrection(contextKey, "Downloads/", "Documents/", at.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := data.PersonalCandidates(contextKey, "Do", 10)
	if len(got) != 1 || got[0].Value != "Downloads/" || got[0].Wins != 3 || got[0].Losses != 0 {
		t.Fatalf("personal candidates = %+v", got)
	}
	if correction, ok := data.Correction(contextKey, "Documents/"); !ok || correction.Losses != 3 {
		t.Fatalf("rejected correction = %+v ok=%v", correction, ok)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := data.Record("git\x00\x00sub\x000\x00commit", at); err != nil {
		t.Fatal(err)
	}
	if err := data.RecordCorrection("cd\x00\x00pos\x000", "work/", "tmp/", at); err != nil {
		t.Fatal(err)
	}
	exported, err := data.Export()
	if err != nil {
		t.Fatal(err)
	}
	otherDir := t.TempDir()
	if err := os.Chmod(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(otherDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Import(exported); err != nil {
		t.Fatal(err)
	}
	if selection, ok := other.Lookup("git\x00\x00sub\x000\x00commit"); !ok || selection.Count != 1 {
		t.Fatalf("imported selection = %+v ok=%v", selection, ok)
	}
	if correction, ok := other.Correction("cd\x00\x00pos\x000", "work/"); !ok || correction.Wins != 1 {
		t.Fatalf("imported correction = %+v ok=%v", correction, ok)
	}
}

func TestMigratesLegacyLearningFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Library", "Application Support", "offline-complete"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, "Library", "Application Support", "offline-complete", "learning.json")
	if err := os.WriteFile(legacy, []byte(`{"schemaVersion":1,"learning":true,"selections":{"key":{"count":2,"last":"2026-11-10T12:00:00Z"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := store.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(home, ".oc") {
		t.Fatalf("default dir = %q", dir)
	}
	data, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if selection, ok := data.Lookup("key"); !ok || selection.Count != 2 {
		t.Fatalf("migrated selection = %+v ok=%v", selection, ok)
	}
	if _, err := os.Stat(filepath.Join(home, ".oc", "learning.json")); err != nil {
		t.Fatal(err)
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
