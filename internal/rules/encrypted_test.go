package rules

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptRoundTripAndRejectsTampering(t *testing.T) {
	plain := []byte(`{"schemaVersion":1,"commands":[]}`)
	sealed, err := Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sealed, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := Decrypt(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decrypted = %q", got)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := Decrypt(sealed); err == nil {
		t.Fatal("accepted modified ciphertext")
	}
}

func TestLoadFileReadsEncryptedPackAndSpec(t *testing.T) {
	dir := t.TempDir()
	plain := []byte(`{"schemaVersion":1,"commands":[]}`)
	sealed, err := Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	rulePath := filepath.Join(dir, "rules.bin")
	if err := os.WriteFile(rulePath, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("loaded = %q", got)
	}
	specSealed, err := Encrypt([]byte("export default {}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc.bin"), specSealed, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &SpecStore{Dir: dir}
	if source, err := store.Load("abc"); err != nil || string(source) != "export default {}" {
		t.Fatalf("spec = %q err=%v", source, err)
	}
	for _, id := range []string{"../abc", "a/b", "", "."} {
		if _, err := store.Load(id); err == nil {
			t.Fatalf("accepted unsafe spec id %q", id)
		}
	}
}
