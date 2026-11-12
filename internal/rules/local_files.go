package rules

import (
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// LocalCandidates lists immediate regular files and directories in a caller-
// supplied directory. Names are display data, not shell-escaped insertion text.
// Links and special files are omitted; the caller must establish context and
// validate any eventual insertion independently.
func LocalCandidates(directory, prefix string, directoriesOnly bool) ([]Candidate, error) {
	if len(prefix) > 65536 || strings.Contains(prefix, "/") || !utf8.ValidString(prefix) || strings.IndexFunc(prefix, unicode.IsControl) >= 0 {
		return nil, errors.New("prefix must be a valid single path component within limits")
	}
	dir, err := openCandidateRoot(directory)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return listLocalCandidates(dir, prefix, "", directoriesOnly)
}

// LocalPathCandidates completes relative paths below a caller-supplied trusted
// directory. Returned names are display data, not shell insertion text.
// The trusted root may itself resolve symlinks; each child directory is opened
// relative to its parent with the final component rejected if it is a link.
func LocalPathCandidates(directory, prefix string, directoriesOnly bool) ([]Candidate, error) {
	if len(prefix) > 65536 {
		return nil, errors.New("path prefix exceeds limits")
	}
	if !utf8.ValidString(prefix) || strings.IndexFunc(prefix, unicode.IsControl) >= 0 || strings.HasPrefix(prefix, "/") {
		return nil, errors.New("invalid relative path prefix")
	}
	if strings.Count(prefix, "/") > 64 {
		return nil, errors.New("path prefix exceeds limits")
	}
	parts := strings.Split(prefix, "/")
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			return nil, errors.New("invalid path component")
		}
	}
	if parts[len(parts)-1] == "." || parts[len(parts)-1] == ".." {
		return nil, errors.New("invalid path component")
	}
	dir, err := openCandidateRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() { dir.Close() }()
	for _, part := range parts[:len(parts)-1] {
		next, err := openLocalPathComponent(dir, part)
		if err != nil {
			return nil, err
		}
		dir.Close()
		dir = next
	}
	pathPrefix := strings.Join(parts[:len(parts)-1], "/")
	if pathPrefix != "" {
		pathPrefix += "/"
	}
	return listLocalCandidates(dir, parts[len(parts)-1], pathPrefix, directoriesOnly)
}

// openCandidateRoot follows the caller's trusted root path, but never opens a
// non-directory such as a FIFO and waits for a peer.
func openCandidateRoot(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openLocalPathComponent(parent *os.File, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return nil, errors.New("invalid path component")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// localCandidateEntry resolves metadata relative to the opened directory and
// never follows a final symlink. DirEntry.Type may be unknown on some filesystems.
func localCandidateEntry(dir *os.File, name string, directoriesOnly bool) (string, bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", false, &os.PathError{Op: "fstatat", Path: name, Err: err}
	}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return name + "/", true, nil
	case unix.S_IFREG:
		if !directoriesOnly {
			return name, true, nil
		}
	}
	return "", false, nil
}

var readCandidateDir = func(dir *os.File, n int) ([]os.DirEntry, error) {
	return dir.ReadDir(n)
}

func listLocalCandidates(dir *os.File, prefix, pathPrefix string, directoriesOnly bool) ([]Candidate, error) {
	var candidates []Candidate
	count, bytes := 0, 0
	for {
		entries, readErr := readCandidateDir(dir, 256)
		count += len(entries)
		if count > 10000 {
			return nil, errors.New("directory contains too many entries")
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, prefix) || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
				continue
			}
			name, ok, err := localCandidateEntry(dir, name, directoriesOnly)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			bytes += len(pathPrefix) + len(name)
			if bytes > 65536 {
				return nil, errors.New("candidate names exceed limit")
			}
			candidates = append(candidates, Candidate{Name: pathPrefix + name})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates, nil
}
