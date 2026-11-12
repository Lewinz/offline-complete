// Package inventory produces a read-only manifest of local rule files.
// Traversal is confined to an opened root and rejects files that change while read.
package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// File records one inventoried rule file.
type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Root records the outcome for one scanned directory.
type Root struct {
	Root            string   `json:"root"`
	Status          string   `json:"status"`
	Error           string   `json:"error,omitempty"`
	Files           []File   `json:"files"`
	SkippedSymlinks []string `json:"skippedSymlinks,omitempty"`
}

// Result is the inventory of all requested roots.
type Result struct {
	Roots []Root `json:"roots"`
}

type checkedFile struct {
	name string
	info os.FileInfo
}

var extensions = map[string]bool{".js": true, ".json": true, ".ts": true}

// Limit both memory use and work per file when scanning untrusted local data.
const maxRuleBytes int64 = 16 << 20

// Bound the aggregate amount read from rule files in a single root.
const maxRootRuleBytes int64 = 64 << 20

// Bound recursion and the number of simultaneously open directory handles.
const maxDirectoryDepth = 64

// Count all directory entries, including files outside the extension filter.
const maxDirectoryEntries = 10000

// Test hooks keep race behaviour observable without weakening production paths.
var openForRead = func(root *os.File, name, path string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return nil, fmt.Errorf("invalid rule file name")
	}
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openDirectory atomically rejects a final-component symlink. The root itself
// is opened as "." relative to its already anchored directory.
func openDirectory(root *os.File, name, path string) (*os.File, error) {
	if name == "" || name == ".." || strings.ContainsRune(name, '/') {
		return nil, fmt.Errorf("invalid directory name")
	}
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openRootDirectory rejects a non-directory or final symlink in the open itself.
func openRootDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

var afterRead = func(string) {}
var beforeReadDir = func(string) {}
var beforeOpenRoot = func(string) {}
var beforeRootRechecks = func() {}

// Scan inventories each root in order. A missing root and an unreadable root
// are reported separately, never as an empty inventory.
func Scan(roots []string) Result {
	result := Result{Roots: make([]Root, 0, len(roots))}
	for _, input := range roots {
		result.Roots = append(result.Roots, scanRoot(input))
	}
	return result
}

func scanRoot(input string) Root {
	if input == "" {
		return Root{Root: input, Status: "error", Error: "root path is empty", Files: []File{}}
	}
	root, err := filepath.Abs(input)
	if err != nil {
		return Root{Root: input, Status: "error", Error: err.Error(), Files: []File{}}
	}
	info, err := os.Lstat(root)
	if err != nil {
		status := "error"
		if os.IsNotExist(err) {
			status = "missing"
		}
		return Root{Root: root, Status: status, Error: err.Error(), Files: []File{}}
	}
	if !info.IsDir() {
		return Root{Root: root, Status: "error", Error: "root is not a directory", Files: []File{}}
	}

	beforeOpenRoot(root)
	rootHandle, err := openRootDirectory(root)
	if err != nil {
		return Root{Root: root, Status: "error", Error: err.Error(), Files: []File{}}
	}
	defer rootHandle.Close()
	rootInfo, err := rootHandle.Stat()
	if err != nil || !rootInfo.IsDir() || !os.SameFile(info, rootInfo) {
		return Root{Root: root, Status: "error", Error: "root replaced while opening", Files: []File{}}
	}
	files := []File{}
	skipped := []string{}
	entriesSeen := 0
	var bytesSeen int64
	var rootChecked, checkedDirs []checkedFile
	if err := scanDir(rootHandle, ".", root, root, info, 0, &entriesSeen, &bytesSeen, &files, &skipped, &rootChecked, &checkedDirs); err != nil {
		return Root{Root: root, Status: "error", Error: err.Error(), Files: []File{}}
	}
	beforeRootRechecks()
	// Earlier sibling directories may have changed since their local checks.
	for _, file := range rootChecked {
		if err := recheckFile(rootHandle, file); err != nil {
			return Root{Root: root, Status: "error", Error: "file changed while scanning root: " + file.name, Files: []File{}}
		}
	}
	for _, dir := range checkedDirs {
		if err := recheckDirectory(rootHandle, dir); err != nil {
			return Root{Root: root, Status: "error", Error: "directory changed while scanning root: " + dir.name, Files: []File{}}
		}
	}
	// The root itself can change after scanDir finishes while descendants are
	// being rechecked; its identity alone does not detect added entries.
	finalRoot, err := rootHandle.Stat()
	if err != nil || !rootInfo.ModTime().Equal(finalRoot.ModTime()) || !changeTime(rootInfo).Equal(changeTime(finalRoot)) {
		return Root{Root: root, Status: "error", Error: "root changed while scanning", Files: []File{}}
	}
	// An ancestor can be renamed without changing the opened root's metadata.
	// Rechecking the path catches persistent replacement, not transient races.
	current, err := os.Lstat(root)
	if err != nil || !current.IsDir() || !os.SameFile(info, current) {
		return Root{Root: root, Status: "error", Error: "root replaced while scanning", Files: []File{}}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(skipped)
	return Root{Root: root, Status: "ok", Files: files, SkippedSymlinks: skipped}
}

// recheckFile resolves each intermediate component without following symlinks.
// It compares the final directory entry without opening a potentially special file.
func recheckFile(root *os.File, file checkedFile) error {
	parts := strings.Split(file.name, string(filepath.Separator))
	dir := int(root.Fd())
	owned := -1
	defer func() {
		if owned >= 0 {
			unix.Close(owned)
		}
	}()
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid relative path")
		}
		if i == len(parts)-1 {
			var current unix.Stat_t
			if err := unix.Fstatat(dir, part, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			previous, ok := file.info.Sys().(*syscall.Stat_t)
			if !ok || current.Mode&unix.S_IFMT != unix.S_IFREG ||
				uint64(current.Dev) != uint64(previous.Dev) || current.Ino != previous.Ino ||
				current.Size != file.info.Size() ||
				!time.Unix(current.Mtim.Sec, current.Mtim.Nsec).Equal(file.info.ModTime()) {
				return fmt.Errorf("file metadata changed")
			}
			ctime := changeTime(file.info)
			if !ctime.IsZero() && !time.Unix(current.Ctim.Sec, current.Ctim.Nsec).Equal(ctime) {
				return fmt.Errorf("file metadata changed")
			}
			return nil
		}
		next, err := unix.Openat(dir, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		if owned >= 0 {
			unix.Close(owned)
		}
		owned, dir = next, next
	}
	return fmt.Errorf("invalid relative path")
}

// recheckDirectory walks from the original root without following links and
// compares the final directory's identity and metadata with the scanned one.
func recheckDirectory(root *os.File, checked checkedFile) error {
	dir := int(root.Fd())
	owned := -1
	defer func() {
		if owned >= 0 {
			unix.Close(owned)
		}
	}()
	for _, part := range strings.Split(checked.name, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid relative path")
		}
		next, err := unix.Openat(dir, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		if owned >= 0 {
			unix.Close(owned)
		}
		owned, dir = next, next
	}
	// Duplicate ownership is avoided: os.File owns the descriptor after this point.
	handle := os.NewFile(uintptr(owned), checked.name)
	owned = -1
	defer handle.Close()
	current, err := handle.Stat()
	if err != nil || !os.SameFile(checked.info, current) ||
		!checked.info.ModTime().Equal(current.ModTime()) || !changeTime(checked.info).Equal(changeTime(current)) {
		return fmt.Errorf("directory metadata changed")
	}
	return nil
}

func scanDir(parent *os.File, name, directory, root string, expected os.FileInfo, depth int, entriesSeen *int, bytesSeen *int64, files *[]File, skipped *[]string, rootChecked, checkedDirs *[]checkedFile) error {
	if depth > maxDirectoryDepth {
		return fmt.Errorf("directory depth exceeds limit: %s", directory)
	}
	beforeReadDir(directory)
	handle, err := openDirectory(parent, name, directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() || !os.SameFile(expected, info) {
		return fmt.Errorf("directory replaced or not a directory: %s", directory)
	}
	if depth > 0 {
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return err
		}
		*checkedDirs = append(*checkedDirs, checkedFile{name: relative, info: info})
	}
	var checked []checkedFile
	var entries []os.FileInfo
	for {
		batch, readErr := handle.Readdir(256)
		*entriesSeen += len(batch)
		if *entriesSeen > maxDirectoryEntries {
			return fmt.Errorf("directory entries exceed limit: %s", root)
		}
		entries = append(entries, batch...)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		entryType := entry.Mode().Type()
		if entryType&os.ModeSymlink != 0 {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			*skipped = append(*skipped, relative)
			continue
		}
		if entry.IsDir() {
			if err := scanDir(handle, entry.Name(), path, root, entry, depth+1, entriesSeen, bytesSeen, files, skipped, rootChecked, checkedDirs); err != nil {
				return err
			}
			continue
		}
		if !entryType.IsRegular() {
			continue
		}
		if !extensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			continue
		}
		name := entry.Name()
		info := entry
		file, err := inspect(handle, name, path, info, maxRootRuleBytes-*bytesSeen)
		if err != nil {
			return err
		}
		*bytesSeen += file.Bytes
		checked = append(checked, checkedFile{name: name, info: info})
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		*rootChecked = append(*rootChecked, checkedFile{name: relative, info: info})
		file.Path = relative
		*files = append(*files, file)
	}
	for _, file := range checked {
		if err := recheckFile(handle, file); err != nil {
			return fmt.Errorf("file changed while scanning directory: %s", filepath.Join(directory, file.name))
		}
	}
	after, err := handle.Stat()
	if err != nil {
		return err
	}
	if !info.ModTime().Equal(after.ModTime()) || !changeTime(info).Equal(changeTime(after)) {
		return fmt.Errorf("directory changed while scanning: %s", directory)
	}
	return nil
}

func inspect(anchor *os.File, name, path string, expected os.FileInfo, remaining int64) (File, error) {
	if !expected.Mode().IsRegular() {
		return File{}, fmt.Errorf("file replaced or not regular: %s", path)
	}
	handle, err := openForRead(anchor, name, path)
	if err != nil {
		return File{}, err
	}
	defer handle.Close()

	before, err := handle.Stat()
	if err != nil {
		return File{}, err
	}
	if !before.Mode().IsRegular() || !os.SameFile(expected, before) {
		return File{}, fmt.Errorf("file replaced or not regular: %s", path)
	}
	if expected.Size() != before.Size() || !expected.ModTime().Equal(before.ModTime()) || !changeTime(expected).Equal(changeTime(before)) {
		return File{}, fmt.Errorf("file changed before reading: %s", path)
	}
	// Retain the post-open check for replacement after open. Openat above
	// rejects a final-component symlink atomically, but cannot make the
	// subsequent path checks or file contents a snapshot.
	if err := recheckFile(anchor, checkedFile{name: name, info: expected}); err != nil {
		return File{}, fmt.Errorf("file replaced or not regular: %s", path)
	}
	if before.Size() > maxRuleBytes {
		return File{}, fmt.Errorf("rule file too large: %s", path)
	}
	if before.Size() > remaining {
		return File{}, fmt.Errorf("total rule bytes exceed limit: %s", path)
	}
	limit := min(maxRuleBytes, remaining)
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(handle, limit+1))
	if err != nil {
		return File{}, err
	}
	if count > maxRuleBytes {
		return File{}, fmt.Errorf("rule file too large: %s", path)
	}
	if count > remaining {
		return File{}, fmt.Errorf("total rule bytes exceed limit: %s", path)
	}
	afterRead(path)
	after, err := handle.Stat()
	if err != nil {
		return File{}, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !changeTime(before).Equal(changeTime(after)) {
		return File{}, fmt.Errorf("file changed while reading: %s", path)
	}
	return File{Bytes: count, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}
