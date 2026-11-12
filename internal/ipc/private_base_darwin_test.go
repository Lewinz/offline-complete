//go:build darwin

package ipc

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestEnsurePrivateBaseCreatesUsableDirectory(t *testing.T) {
	parent := privateBase(t)
	base := filepath.Join(parent, "ipc")
	if err := EnsurePrivateBase(base); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("private base: %v, %v", info, err)
	}
	if err := EnsurePrivateBase(base); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	listener, err := NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePrivateBaseRejectsUnsafeExistingPaths(t *testing.T) {
	parent := privateBase(t)
	for _, kind := range []string{"file", "symlink", "insecure"} {
		t.Run(kind, func(t *testing.T) {
			base := filepath.Join(parent, kind)
			switch kind {
			case "file":
				if err := os.WriteFile(base, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(parent, base); err != nil {
					t.Fatal(err)
				}
			case "insecure":
				if err := os.Mkdir(base, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(base, 0755); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(base)
			if err != nil {
				t.Fatal(err)
			}
			if err := EnsurePrivateBase(base); err == nil {
				t.Fatal("accepted unsafe base")
			}
			after, err := os.Lstat(base)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("unsafe path changed: %v, %v", after, err)
			}
		})
	}
	if err := EnsurePrivateBase(filepath.Join(parent, "missing", "nested")); err == nil {
		t.Fatal("created missing parent")
	}
}

func TestEnsurePrivateBaseRejectsUnsafeParent(t *testing.T) {
	root := privateBase(t)
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(parent, "ipc")
	if err := EnsurePrivateBase(base); err == nil {
		t.Fatal("accepted non-private parent")
	}
	if _, err := os.Lstat(base); !os.IsNotExist(err) {
		t.Fatalf("created a base under unsafe parent: %v", err)
	}
	if err := EnsurePrivateBase("relative"); err == nil {
		t.Fatal("accepted relative path")
	}
}

func TestEnsurePrivateBaseRejectsRestrictiveUmask(t *testing.T) {
	if os.Getenv("IPC_BASE_UMASK_TEST") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestEnsurePrivateBaseRejectsRestrictiveUmask$")
		cmd.Env = append(os.Environ(), "IPC_BASE_UMASK_TEST=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated test: %v\n%s", err, out)
		}
		return
	}
	parent := privateBase(t)
	syscall.Umask(0777)
	base := filepath.Join(parent, "ipc")
	if err := EnsurePrivateBase(base); err == nil {
		t.Fatal("accepted inaccessible base")
	}
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0 {
		t.Fatalf("created directory not preserved: %v, %v", info, err)
	}
}
