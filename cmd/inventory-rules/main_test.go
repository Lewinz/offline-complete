package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"offline-complete/internal/inventory"
)

func TestRejectsNonUTF8RootWithoutWritingManifest(t *testing.T) {
	if os.Getenv("INVENTORY_INVALID_ROOT_HELPER") == "1" {
		os.Args = []string{"inventory-rules", "invalid-\xff"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRejectsNonUTF8RootWithoutWritingManifest$")
	cmd.Env = append(os.Environ(), "INVENTORY_INVALID_ROOT_HELPER=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("invalid UTF-8 root was accepted")
	}
	if stdout.Len() != 0 {
		t.Fatalf("lossy manifest was written: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "UTF-8") {
		t.Fatalf("missing UTF-8 diagnostic: %q", stderr.String())
	}
}

func TestManifestRejectsInvalidUTF8InScannedPaths(t *testing.T) {
	cases := []inventory.Root{
		{Root: "/safe", Status: "ok", Files: []inventory.File{{Path: "bad-\xff.json"}}},
		{Root: "/safe", Status: "ok", SkippedSymlinks: []string{"bad-\xff"}},
		{Root: "/safe", Status: "error", Error: "read bad-\xff"},
	}
	for _, root := range cases {
		encoded, err := encodeManifest(inventory.Result{Roots: []inventory.Root{root}})
		if err == nil || len(encoded) != 0 {
			t.Fatalf("invalid inventory serialized: %q, %v", encoded, err)
		}
	}
}

func TestOutputFailureReturnsNonzero(t *testing.T) {
	if os.Getenv("INVENTORY_OUTPUT_FAILURE_HELPER") == "1" {
		os.Args = []string{"inventory-rules", os.Getenv("INVENTORY_TEST_ROOT")}
		main()
		return
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rule.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	// A directory opened for reading cannot accept the JSON output.
	output, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutputFailureReturnsNonzero$")
	cmd.Env = append(os.Environ(), "INVENTORY_OUTPUT_FAILURE_HELPER=1", "INVENTORY_TEST_ROOT="+root)
	cmd.Stdout = output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		t.Fatal("inventory succeeded despite failed output")
	}
	if !strings.Contains(stderr.String(), "inventory-rules:") {
		t.Fatalf("missing output error diagnostic: %q", stderr.String())
	}
}
