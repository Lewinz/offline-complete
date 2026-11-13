package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func TestLocalPathComponentRejectsReplacementSymlink(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "original"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original", filepath.Join(base, "component")); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	child, err := openLocalPathComponent(dir, "component")
	if err == nil {
		child.Close()
		t.Fatal("opened a symlink instead of rejecting the replacement")
	}
	child, err = openLocalPathComponent(dir, "original")
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
}

func TestCandidatesUseOpenedDirectoryAfterRename(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "current")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "item"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.Rename(path, filepath.Join(base, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "item"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := listLocalCandidates(dir, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{Name: "item"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// A directory entry whose type is unknown must not be assumed regular. The
// lookup must use the open directory, even if its original path is replaced.
func TestCandidateEntryTypeUsesOpenedDirectory(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "current")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(path, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.Rename(path, filepath.Join(base, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "file"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		ok         bool
	}{
		{"file", "file", true}, {"dir", "dir/", true}, {"link", "", false}, {"fifo", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := localCandidateEntry(dir, tc.name, false)
			if err != nil || got != tc.want || ok != tc.ok {
				t.Fatalf("got %q, %v, %v; want %q, %v", got, ok, err, tc.want, tc.ok)
			}
		})
	}
}

// unknownTypeEntry simulates filesystems that do not provide d_type.
type unknownTypeEntry struct{ os.DirEntry }

func (unknownTypeEntry) Type() os.FileMode { return 0 }
func (unknownTypeEntry) IsDir() bool       { return false }

func TestLocalCandidatesUnknownEntryTypes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	old := readCandidateDir
	t.Cleanup(func() { readCandidateDir = old })
	readCandidateDir = func(dir *os.File, n int) ([]os.DirEntry, error) {
		entries, err := old(dir, n)
		for i, entry := range entries {
			entries[i] = unknownTypeEntry{entry}
		}
		return entries, err
	}
	got, err := LocalCandidates(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{Name: "dir/"}, {Name: "file"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestLocalCandidatesIgnoreCase(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "Alpha"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LocalCandidates(base, "al", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Alpha" {
		t.Fatalf("got %#v", got)
	}
}
