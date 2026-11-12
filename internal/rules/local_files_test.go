package rules_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"offline-complete/internal/rules"
)

func TestCandidateRootFIFOIsRejectedWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		call func() ([]rules.Candidate, error)
	}{
		{"direct", func() ([]rules.Candidate, error) { return rules.LocalCandidates(fifo, "", false) }},
		{"relative", func() ([]rules.Candidate, error) { return rules.LocalPathCandidates(fifo, "", false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { _, err := tc.call(); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("accepted FIFO as a directory")
				}
			case <-time.After(300 * time.Millisecond):
				fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
				if err == nil {
					syscall.Close(fd)
				}
				<-done
				t.Fatal("opening FIFO blocked")
			}
		})
	}
}

func TestCandidateRootAllowsTrustedSymlinkAndRejectsFile(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "item")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func(string) ([]rules.Candidate, error){
		func(path string) ([]rules.Candidate, error) { return rules.LocalCandidates(path, "", false) },
		func(path string) ([]rules.Candidate, error) { return rules.LocalPathCandidates(path, "", false) },
	} {
		got, err := call(link)
		if err != nil || !reflect.DeepEqual(got, []rules.Candidate{{Name: "item"}}) {
			t.Fatalf("trusted symlink: got %#v, error %v", got, err)
		}
		if got, err := call(file); err == nil || len(got) != 0 {
			t.Fatalf("regular file accepted: got %#v, error %v", got, err)
		}
	}
}

func TestLocalCandidatesOnlyMatchImmediateEntries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha file", "alpine", "beta"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "alpha-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("alpine", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := rules.LocalCandidates(dir, "al", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []rules.Candidate{{Name: "alpha file"}, {Name: "alpha-dir/"}, {Name: "alpine"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	got, err = rules.LocalCandidates(dir, "al", true)
	if err != nil {
		t.Fatal(err)
	}
	want = []rules.Candidate{{Name: "alpha-dir/"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("directories: got %#v, want %#v", got, want)
	}
}

func TestLocalCandidatesRejectsOversizedDirectory(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10001; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rules.LocalCandidates(dir, "", false)
	if err == nil || len(got) != 0 {
		t.Fatalf("oversized directory: got %#v, error %v", got, err)
	}
}

func TestLocalCandidatesRejectsUnsafeOrOversizedPrefix(t *testing.T) {
	dir := t.TempDir()
	for _, prefix := range []string{"a\nb", "a\x00b", strings.Repeat("a", 65537)} {
		if got, err := rules.LocalCandidates(dir, prefix, false); err == nil || len(got) != 0 {
			t.Errorf("accepted unsafe or oversized prefix (length %d): %#v, %v", len(prefix), got, err)
		}
	}
	if _, err := rules.LocalCandidates(dir, strings.Repeat("a", 65536), false); err != nil {
		t.Fatalf("rejected prefix at limit: %v", err)
	}
}

func TestLocalCandidatesRejectsPathsAndReportsMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, prefix := range []string{"../", "foo/bar", "/"} {
		if _, err := rules.LocalCandidates(dir, prefix, false); err == nil {
			t.Errorf("accepted prefix %q", prefix)
		}
	}
	if _, err := rules.LocalCandidates(filepath.Join(dir, "missing"), "", false); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestLocalPathCandidatesNestedAndConfined(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "alpha file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested", "alpine"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nested", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := rules.LocalPathCandidates(root, "nested/al", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []rules.Candidate{{Name: "nested/alpha file"}, {Name: "nested/alpine/"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for _, prefix := range []string{"../al", "/tmp/al", "nested/../al", "link/al", "nested//al"} {
		if got, err := rules.LocalPathCandidates(root, prefix, false); err == nil {
			t.Errorf("accepted %q: %#v", prefix, got)
		}
	}
}

func TestLocalPathCandidatesRejectsOversizedPrefixWithoutLargeAllocation(t *testing.T) {
	prefix := strings.Repeat("a/", 512*1024)
	root := t.TempDir()
	if got, err := rules.LocalPathCandidates(root, prefix, false); err == nil || len(got) != 0 {
		t.Fatalf("oversized prefix: candidates=%v error=%v", got, err)
	}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = rules.LocalPathCandidates(root, prefix, false)
		}
	})
	if allocated := result.AllocedBytesPerOp(); allocated > 512*1024 {
		t.Fatalf("oversized prefix allocated %d bytes per call", allocated)
	}
}

func TestLocalPathCandidatesRejectsExcessiveDepthWithoutLargeAllocation(t *testing.T) {
	prefix := strings.Repeat("a/", 32768)
	root := t.TempDir()
	if got, err := rules.LocalPathCandidates(root, prefix, false); err == nil || len(got) != 0 {
		t.Fatalf("excessive depth: candidates=%v error=%v", got, err)
	}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = rules.LocalPathCandidates(root, prefix, false)
		}
	})
	if allocated := result.AllocedBytesPerOp(); allocated > 256*1024 {
		t.Fatalf("excessive depth allocated %d bytes per call", allocated)
	}
}

func TestLocalPathCandidatesRejectsExcessiveDepth(t *testing.T) {
	root := t.TempDir()
	dir := root
	for i := 0; i < 65; i++ {
		dir = filepath.Join(dir, "d")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rules.LocalPathCandidates(root, strings.Repeat("d/", 65), false)
	if err == nil || len(got) != 0 {
		t.Fatalf("excessive depth accepted: %#v, %v", got, err)
	}
}

func TestLocalPathCandidatesRejectsOversizedResult(t *testing.T) {
	root := t.TempDir()
	dir := root
	var prefix string
	for i := 0; i < 4; i++ {
		component := strings.Repeat("d", 100)
		dir = filepath.Join(dir, component)
		prefix += component + "/"
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rules.LocalPathCandidates(root, prefix, false)
	if err == nil || len(got) != 0 {
		t.Fatalf("oversized result returned %d candidates, error %v", len(got), err)
	}
}

func TestLocalPathCandidatesDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub", "album"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "alpha"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("album", filepath.Join(root, "sub", "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := rules.LocalPathCandidates(root, "sub/al", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []rules.Candidate{{Name: "sub/album/"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
