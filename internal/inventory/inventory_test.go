package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScanOpenedRootAfterPathReplaced(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "rules")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "safe.js"), "safe")
	handle, err := openRootDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "outside.js"), "outside")
	var files []File
	var skipped []string
	var entries int
	var bytes int64
	var checked, dirs []checkedFile
	err = scanDir(handle, ".", root, root, info, 0, &entries, &bytes, &files, &skipped, &checked, &dirs)
	if err != nil || len(files) != 1 || files[0].Path != "safe.js" {
		t.Fatalf("opened root scan = %+v, %v; want only safe.js", files, err)
	}
}

func TestOpenRootDirectoryRejectsNonDirectoriesAndFinalSymlink(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	write(t, file, "rule")
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, file, link} {
		f, err := openRootDirectory(path)
		if err == nil {
			f.Close()
			t.Fatalf("opened non-directory or symlink %s", path)
		}
	}
	f, err := openRootDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestRuleOpenDoesNotFollowFinalSymlink(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "actual.js"), "rule")
	if err := os.Symlink("actual.js", filepath.Join(root, "link.js")); err != nil {
		t.Fatal(err)
	}
	anchor, err := openRootDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()
	file, err := openForRead(anchor, "link.js", filepath.Join(root, "link.js"))
	if err == nil {
		file.Close()
		t.Fatal("opened a final-component symlink")
	}
}

func TestRejectsRootChangedDuringFinalRechecks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "first.js"), "first")
	original := beforeRootRechecks
	t.Cleanup(func() { beforeRootRechecks = original })
	beforeRootRechecks = func() {
		write(t, filepath.Join(root, "late.js"), "late")
	}

	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v; want error without partial inventory", got)
	}
}

func TestEmptyRootDoesNotScanWorkingDirectory(t *testing.T) {
	opened := false
	old := beforeOpenRoot
	beforeOpenRoot = func(string) { opened = true }
	defer func() { beforeOpenRoot = old }()

	got := Scan([]string{""}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 || opened {
		t.Fatalf("empty root: result=%+v opened=%v; want error without scanning", got, opened)
	}
	got = Scan([]string{"."}).Roots[0]
	if got.Status != "ok" || !opened {
		t.Fatalf("explicit current directory: result=%+v opened=%v", got, opened)
	}
}

func TestInventoriesRuleFilesWithoutFollowingSymlinksOrReadingUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	specs := filepath.Join(root, "specs")
	if err := os.Mkdir(specs, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(specs, "git.js"), "abc")
	write(t, filepath.Join(specs, "history"), "private")
	if err := os.Symlink(filepath.Join(specs, "git.js"), filepath.Join(specs, "link.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(specs, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	result := Scan([]string{specs, filepath.Join(root, "missing")})
	if len(result.Roots) != 2 {
		t.Fatalf("roots = %d", len(result.Roots))
	}
	first := result.Roots[0]
	if first.Status != "ok" {
		t.Fatalf("status = %q, error = %q", first.Status, first.Error)
	}
	if len(first.Files) != 1 || first.Files[0].Path != "git.js" {
		t.Fatalf("files = %+v", first.Files)
	}
	sum := sha256.Sum256([]byte("abc"))
	if first.Files[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %s", first.Files[0].SHA256)
	}
	if strings.Join(first.SkippedSymlinks, ",") != "link.js,linked-dir" {
		t.Fatalf("skipped = %v", first.SkippedSymlinks)
	}
	if result.Roots[1].Status != "missing" {
		t.Fatalf("missing root status = %q", result.Roots[1].Status)
	}
}

func TestTotalRuleBytesAcrossDirectoriesFailWithoutPartialInventory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		dir := root
		if i >= 2 {
			dir = sub
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%d.js", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(16 << 20); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 || !strings.Contains(got.Error, "total") {
		t.Fatalf("root = %+v, want total byte limit error without partial inventory", got)
	}
}

func TestTotalRuleByteLimitAllowsBoundaryAndResetsPerRoot(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		f, err := os.Create(filepath.Join(root, fmt.Sprintf("%d.js", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(16 << 20); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	other := t.TempDir()
	write(t, filepath.Join(other, "small.js"), "ok")
	got := Scan([]string{root, other}).Roots
	if got[0].Status != "ok" || len(got[0].Files) != 4 || got[1].Status != "ok" || len(got[1].Files) != 1 {
		t.Fatalf("roots = %+v, want boundary accepted and separate root budget", got)
	}
}

func TestExcessiveDirectoryEntriesFailWithoutPartialInventory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "rule.js"), "rule")
	for i := 0; i < 10000; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("ignored-%05d", i)), "")
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 || !strings.Contains(got.Error, "entries") {
		t.Fatalf("root = %+v, want entry limit error without partial inventory", got)
	}
}

func TestDirectoryEntryLimitCountsAcrossNestedDirectories(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		name := fmt.Sprintf("ignored-%05d", i)
		write(t, filepath.Join(root, name), "")
		write(t, filepath.Join(sub, name), "")
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 || !strings.Contains(got.Error, "entries") {
		t.Fatalf("root = %+v, want total entry limit error", got)
	}
}

func TestDirectoryEntriesAtLimitAreInventoried(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "rule.js"), "rule")
	for i := 0; i < 9999; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("ignored-%05d", i)), "")
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "ok" || len(got.Files) != 1 || got.Files[0].Path != "rule.js" {
		t.Fatalf("root = %+v, want one rule at entry limit", got)
	}
}

func TestDirectoryChangedDuringScanFailsWithoutPartialInventory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "first.js"), "rule")
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	afterRead = func(string) {
		write(t, filepath.Join(root, "added.js"), "new rule")
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v, want directory change error without partial inventory", got)
	}
}

func TestExcessiveDirectoryDepthFailsWithoutPartialInventory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "first.js"), "rule")
	dir := root
	for i := 0; i < 65; i++ {
		dir = filepath.Join(dir, "d")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "deep.js"), "rule")
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 || !strings.Contains(got.Error, "depth") {
		t.Fatalf("root = %+v, want depth error without partial files", got)
	}
}

func TestDirectoryAtDepthLimitIsInventoried(t *testing.T) {
	root := t.TempDir()
	dir := root
	for i := 0; i < 64; i++ {
		dir = filepath.Join(dir, "d")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "rule.js"), "rule")
	got := Scan([]string{root}).Roots[0]
	if got.Status != "ok" || len(got.Files) != 1 {
		t.Fatalf("root = %+v, want one file", got)
	}
}

func TestPreviouslyReadFileChangedDuringDirectoryScanFails(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.js"), "first")
	write(t, filepath.Join(root, "b.js"), "second")
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	var first string
	afterRead = func(path string) {
		if first == "" {
			first = path
			return
		}
		write(t, first, "changed after reading")
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v, want error without stale inventory", got)
	}
}

func TestFileChangedBetweenInspectionAndOpenFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rule.js")
	write(t, path, "before")
	original := openForRead
	t.Cleanup(func() { openForRead = original })
	openForRead = func(anchor *os.File, name, path string) (*os.File, error) {
		write(t, path, "after!")
		stamp := time.Unix(1000000000, 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return original(anchor, name, path)
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v, want changed file error without partial inventory", got)
	}
}

func TestInspectRejectsSymlinkToSameFileBeforeReading(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rule.js")
	write(t, path, "private")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := openRootDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()
	originalOpen, originalAfterRead := openForRead, afterRead
	t.Cleanup(func() { openForRead, afterRead = originalOpen, originalAfterRead })
	read := false
	afterRead = func(string) { read = true }
	openForRead = func(root *os.File, name, path string) (*os.File, error) {
		if err := os.Rename(path, filepath.Join(filepath.Dir(path), "original")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("original", path); err != nil {
			t.Fatal(err)
		}
		return originalOpen(root, name, path)
	}
	_, err = inspect(anchor, "rule.js", path, info, 1024)
	if err == nil || read {
		t.Fatalf("inspect error = %v, read = %v; want rejection before reading", err, read)
	}
}

func TestInspectRejectsKnownFIFOWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rule.js")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := openRootDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()
	original := openForRead
	t.Cleanup(func() { openForRead = original })
	opened := false
	openForRead = func(root *os.File, name, path string) (*os.File, error) {
		opened = true
		return original(root, name, path)
	}
	_, err = inspect(anchor, "rule.js", path, info, maxRootRuleBytes)
	if err == nil {
		t.Fatal("accepted a FIFO as a rule file")
	}
	if opened {
		t.Fatal("opened a known special file before rejecting it")
	}
}

func TestReportsFilesDisappearingDuringAScanAsErrorsNotMissingRoots(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.js")
	second := filepath.Join(root, "second.js")
	write(t, first, "one")
	write(t, second, "two")

	original := openForRead
	t.Cleanup(func() { openForRead = original })
	removed := false
	openForRead = func(anchor *os.File, name, path string) (*os.File, error) {
		if !removed {
			removed = true
			_ = os.Remove(first)
			_ = os.Remove(second)
		}
		return original(anchor, name, path)
	}

	result := Scan([]string{root})
	if result.Roots[0].Status != "error" {
		t.Fatalf("status = %q", result.Roots[0].Status)
	}
	if !strings.Contains(result.Roots[0].Error, "no such file") {
		t.Fatalf("error = %q", result.Roots[0].Error)
	}
	if len(result.Roots[0].Files) != 0 {
		t.Fatalf("files = %+v", result.Roots[0].Files)
	}
}

func TestRejectsDirectoryReplacedBySymlinkBeforeTraversal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "rules")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.js"), "not a rule")
	original := beforeReadDir
	t.Cleanup(func() { beforeReadDir = original })
	beforeReadDir = func(path string) {
		if path != dir {
			return
		}
		if err := os.Rename(dir, dir+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, dir); err != nil {
			t.Fatal(err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v", got)
	}
}

func TestOpenDirectoryRejectsFinalSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("rules", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	parent, err := openRootDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	handle, err := openDirectory(parent, "rules", filepath.Join(root, "rules"))
	if err != nil {
		t.Fatal(err)
	}
	handle.Close()
	handle, err = openDirectory(parent, "alias", filepath.Join(root, "alias"))
	if err == nil {
		handle.Close()
		t.Fatal("opened directory through final symlink")
	}
}

func TestRejectsDirectoryReplacedBySymlinkToSameDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "rules")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "rule.js"), "rule")
	parent, err := openRootDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := beforeReadDir
	t.Cleanup(func() { beforeReadDir = original })
	beforeReadDir = func(path string) {
		if path != dir {
			return
		}
		if err := os.Rename(dir, dir+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("rules-moved", dir); err != nil {
			t.Fatal(err)
		}
	}
	var files []File
	var skipped []string
	var entries int
	var bytes int64
	var rootChecked, checkedDirs []checkedFile
	err = scanDir(parent, "rules", dir, root, info, 1, &entries, &bytes, &files, &skipped, &rootChecked, &checkedDirs)
	if err == nil || len(files) != 0 {
		t.Fatalf("followed replacement directory symlink: files=%+v err=%v", files, err)
	}
}

func TestRejectsRootReplacedBySymlinkToSameDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "rules")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "rule.js"), "rule")
	original := beforeOpenRoot
	t.Cleanup(func() { beforeOpenRoot = original })
	beforeOpenRoot = func(path string) {
		if err := os.Rename(path, path+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path+"-moved", path); err != nil {
			t.Fatal(err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("followed replacement root symlink: %+v", got)
	}
}

func TestDoesNotReadOutsideWhenRootPathIsReplaced(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "rules")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "first.js"), "rule")
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(outside, "nested", "secret.js"), "secret")
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	afterRead = func(path string) {
		if path == filepath.Join(root, "first.js") {
			if err := os.Rename(root, root+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, root); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := Scan([]string{root}).Roots[0]
	for _, file := range got.Files {
		if file.Path == filepath.Join("nested", "secret.js") {
			t.Fatalf("read outside the original root: %+v", got)
		}
	}
}

func TestRejectsRootReplacedAfterFileRead(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	root := filepath.Join(parent, "rules")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "git.js"), "original")
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	afterRead = func(string) {
		if err := os.Rename(parent, parent+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v, want replacement error without partial inventory", got)
	}
}

func TestRejectsIntermediateSymlinkReplacement(t *testing.T) {
	root := t.TempDir()
	branch := filepath.Join(root, "branch")
	target := filepath.Join(root, "target")
	for _, dir := range []string{branch, target} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(branch, "first.js"), "first")
	write(t, filepath.Join(branch, "second.js"), "original")
	write(t, filepath.Join(target, "first.js"), "replacement")
	write(t, filepath.Join(target, "second.js"), "replacement")
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	replaced := false
	afterRead = func(path string) {
		if filepath.Dir(path) == branch && !replaced {
			replaced = true
			if err := os.Rename(branch, branch+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("target", branch); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := Scan([]string{root}).Roots[0]
	for _, file := range got.Files {
		if filepath.Dir(file.Path) == "branch" {
			sum := sha256.Sum256([]byte("replacement"))
			if file.SHA256 == hex.EncodeToString(sum[:]) {
				t.Fatalf("followed replacement symlink: %+v", got)
			}
		}
	}
}

func TestRejectsRuleModifiedAfterSiblingDirectoryScanned(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"first", "second"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, dir, "rule.js"), "original")
	}
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	var first string
	afterRead = func(path string) {
		if first == "" {
			first = path
			return
		}
		if err := os.WriteFile(first, []byte("changed contents"), 0600); err != nil {
			t.Errorf("modify previously scanned sibling: %v", err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("modified sibling must invalidate inventory: %+v", got)
	}
}

func TestRejectsPreviouslyScannedDirectoryReplacedWithHardlinkedCopy(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(root, name, "nested")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "rule.js"), "original")
	}
	// Prepare both copies before scanning so creating the hard link does not
	// alter the already-scanned file's ctime during the scan.
	replacements := t.TempDir()
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(replacements, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(root, name, "nested", "rule.js"), filepath.Join(dir, "rule.js")); err != nil {
			t.Fatal(err)
		}
	}
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	var first string
	afterRead = func(path string) {
		if first == "" {
			first = path
			return
		}
		dir := filepath.Dir(first)
		name := filepath.Base(filepath.Dir(dir))
		if err := os.Rename(dir, dir+"-saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(replacements, name), dir); err != nil {
			t.Fatal(err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("replaced directory must invalidate inventory: %+v", got)
	}
}

func TestRejectsPreviouslyScannedDirectoryReplacedWithSymlink(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(root, name, "nested")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "rule.js"), "original")
	}
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	var first string
	afterRead = func(path string) {
		if first == "" {
			first = path
			return
		}
		dir := filepath.Dir(first)
		if err := os.Rename(dir, dir+"-saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("nested-saved", dir); err != nil {
			t.Fatal(err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("replaced intermediate directory must invalidate inventory: %+v", got)
	}
}

func TestRejectsRuleReplacedWhileBeingRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "git.js")
	replacement := filepath.Join(t.TempDir(), "replacement")
	write(t, path, "original")
	write(t, replacement, "replacement")
	original := afterRead
	t.Cleanup(func() { afterRead = original })
	afterRead = func(name string) {
		if err := os.Rename(replacement, name); err != nil {
			t.Errorf("rename: %v", err)
		}
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v", got)
	}
}

func TestRejectsRuleReplacedBeforeOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "git.js")
	replacement := filepath.Join(t.TempDir(), "replacement")
	write(t, path, "original")
	write(t, replacement, "replacement")
	original := openForRead
	t.Cleanup(func() { openForRead = original })
	openForRead = func(anchor *os.File, name, path string) (*os.File, error) {
		if err := os.Rename(replacement, path); err != nil {
			return nil, err
		}
		return original(anchor, name, path)
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 {
		t.Fatalf("root = %+v", got)
	}
}

func TestRejectsRuleReplacedByFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "git.js")
	write(t, path, "rule")
	original := openForRead
	t.Cleanup(func() { openForRead = original })
	openForRead = func(anchor *os.File, name, path string) (*os.File, error) {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		if err := syscall.Mkfifo(path, 0600); err != nil {
			return nil, err
		}
		return original(anchor, name, path)
	}
	result := make(chan Result, 1)
	go func() { result <- Scan([]string{root}) }()
	select {
	case got := <-result:
		if got.Roots[0].Status != "error" || len(got.Roots[0].Files) != 0 {
			t.Fatalf("root = %+v", got.Roots[0])
		}
	case <-time.After(time.Second):
		// Unblock the old implementation before the test exits.
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = f.Close()
		}
		<-result
		t.Fatal("scan blocked opening a replacement FIFO")
	}
}

func TestRejectsARuleFileModifiedWhileItIsBeingRead(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "git.js")
	write(t, file, "old")

	original := afterRead
	t.Cleanup(func() { afterRead = original })
	afterRead = func(path string) {
		if err := os.WriteFile(path, []byte("new contents"), 0o600); err != nil {
			t.Errorf("rewrite: %v", err)
		}
	}

	result := Scan([]string{root})
	if result.Roots[0].Status != "error" {
		t.Fatalf("status = %q", result.Roots[0].Status)
	}
	if !strings.Contains(result.Roots[0].Error, "file changed while reading") {
		t.Fatalf("error = %q", result.Roots[0].Error)
	}
	if len(result.Roots[0].Files) != 0 {
		t.Fatalf("files = %+v", result.Roots[0].Files)
	}
}

func TestReportsInaccessibleDirectoriesAsErrorsRatherThanEmptyInventories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are not meaningful as root")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	result := Scan([]string{root})
	if result.Roots[0].Status != "error" {
		t.Fatalf("status = %q", result.Roots[0].Status)
	}
	if !strings.Contains(result.Roots[0].Error, "permission denied") {
		t.Fatalf("error = %q", result.Roots[0].Error)
	}
}

func TestAcceptsRuleAtSizeLimit(t *testing.T) {
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "large.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(16 << 20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "ok" || len(got.Files) != 1 || got.Files[0].Bytes != 16<<20 {
		t.Fatalf("root = %+v", got)
	}
}

func TestRejectsOversizedRuleWithoutReturningPartialInventory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "small.js"), "rule")
	f, err := os.Create(filepath.Join(root, "large.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(16<<20 + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got := Scan([]string{root}).Roots[0]
	if got.Status != "error" || len(got.Files) != 0 || !strings.Contains(got.Error, "too large") {
		t.Fatalf("root = %+v", got)
	}
}

func TestReportsARootThatIsNotADirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "rules.json")
	write(t, file, "{}")

	result := Scan([]string{file})
	if result.Roots[0].Status != "error" || !strings.Contains(result.Roots[0].Error, "not a directory") {
		t.Fatalf("root = %+v", result.Roots[0])
	}
}

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
