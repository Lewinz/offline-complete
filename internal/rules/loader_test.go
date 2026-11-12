package rules_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"offline-complete/internal/rules"
)

type countingFS struct {
	fs.FS
	reads int
}

type countingFile struct {
	fs.File
	owner *countingFS
}

func (f *countingFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: file, owner: f}, nil
}

func (f *countingFile) Read(p []byte) (int, error) {
	f.owner.reads++
	return f.File.Read(p)
}

func TestLoaderRejectsOversizedHashWithoutLargeAllocation(t *testing.T) {
	entry := ruleEntry("git.json", "")
	entry.SHA256 = strings.Repeat("a", 4<<20)
	entries := map[string]rules.Entry{"git": entry}
	files := fstest.MapFS{}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := rules.NewLoader(files, entries)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("accepted oversized hash")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("oversized hash allocated %d bytes", allocated)
	}
}

func TestLoaderRejectsKnownOversizedFileWithoutReading(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "git.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((16 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	files := &countingFS{FS: os.DirFS(dir)}
	loader, err := rules.NewLoader(files, map[string]rules.Entry{"git": ruleEntry("git.json", "")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load("git"); err == nil {
		t.Fatal("accepted oversized rule")
	}
	if files.reads != 0 {
		t.Fatalf("read oversized rule %d times", files.reads)
	}
}

func TestLoaderPreservesSourceAndCopiesManifest(t *testing.T) {
	data := `{"schemaVersion":1,"commands":[{"name":"git"}]}`
	entries := map[string]rules.Entry{"git": ruleEntry("git.json", data)}
	loader, err := rules.NewLoader(fstest.MapFS{"git.json": &fstest.MapFile{Data: []byte(data)}}, entries)
	if err != nil {
		t.Fatal(err)
	}
	delete(entries, "git")
	pack, err := loader.Load("git")
	if err != nil {
		t.Fatal(err)
	}
	if pack.Source.Repository != "local-fixture" || pack.Source.Revision != "abc123" || pack.Source.License != "MIT" {
		t.Fatalf("source = %+v", pack.Source)
	}
}

func TestLoaderRejectsInvalidContentWithMatchingHash(t *testing.T) {
	for _, data := range []string{
		`{"schemaVersion":2,"commands":[{"name":"git"}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","extra":1}]}`,
		`{"schemaVersion":1,`,
	} {
		loader, err := rules.NewLoader(fstest.MapFS{"git.json": &fstest.MapFile{Data: []byte(data)}}, map[string]rules.Entry{"git": ruleEntry("git.json", data)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loader.Load("git"); err == nil {
			t.Errorf("accepted invalid content: %s", data)
		}
	}
}

func ruleEntry(path, data string) rules.Entry {
	hash := sha256.Sum256([]byte(data))
	return rules.Entry{Path: path, SHA256: hex.EncodeToString(hash[:]), Source: rules.Source{Repository: "local-fixture", Revision: "abc123", License: "MIT"}}
}

func TestLoadCommandVerifiesIntegrityAndUsesOnlyRequestedFile(t *testing.T) {
	data := `{"schemaVersion":1,"commands":[{"name":"git","options":[{"name":"--version"}]}]}`
	files := fstest.MapFS{"git.json": &fstest.MapFile{Data: []byte(data)}}
	entries := map[string]rules.Entry{"git": ruleEntry("git.json", data), "missing": ruleEntry("absent.json", "irrelevant")}
	loader, err := rules.NewLoader(files, entries)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := loader.Load("git")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(pack.Complete([]string{"git"}, "--")); !equal(got, []string{"--version"}) {
		t.Fatalf("candidates = %v", got)
	}
	if _, err := loader.Load("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := loader.Load("unknown"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unknown command: %v", err)
	}
	files["git.json"].Data = []byte(`{"schemaVersion":1,"commands":[]}`)
	if _, err := loader.Load("git"); err == nil {
		t.Fatal("accepted modified rule file")
	}
}

func TestLoaderRejectsInvalidManifestAndWrongCommand(t *testing.T) {
	data := `{"schemaVersion":1,"commands":[{"name":"other"}]}`
	valid := ruleEntry("git.json", data)
	for name, entry := range map[string]rules.Entry{
		"traversal": {Path: "../git.json", SHA256: valid.SHA256, Source: valid.Source},
		"hash":      {Path: valid.Path, SHA256: "not-a-hash", Source: valid.Source},
		"source":    {Path: valid.Path, SHA256: valid.SHA256},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := rules.NewLoader(fstest.MapFS{}, map[string]rules.Entry{"git": entry}); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	loader, err := rules.NewLoader(fstest.MapFS{"git.json": &fstest.MapFile{Data: []byte(data)}}, map[string]rules.Entry{"git": valid})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load("git"); err == nil {
		t.Fatal("accepted rules for a different command")
	}
}
