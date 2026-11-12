package rules

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SpecStore loads upstream spec modules by opaque identifier or upstream name.
// The installed application keeps these files encrypted; development can point
// the store at a directory of plain .js modules.
type SpecStore struct {
	Dir string

	mu     sync.Mutex
	index  map[string]string
	loaded bool
}

// Load returns the source of one spec module by opaque ID. Identifiers are
// deliberately restricted to a flat filename component; callers cannot turn a
// rule value into an arbitrary path.
func (s *SpecStore) Load(id string) ([]byte, error) {
	if s == nil || s.Dir == "" {
		return nil, errors.New("spec store is not configured")
	}
	if !validSpecID(id) {
		return nil, errors.New("invalid spec id")
	}
	return s.loadID(id)
}

// LoadName resolves an upstream spec name such as "aws/s3" or "az/2.53.0".
// The name is looked up in an encrypted index, never used as a path.
func (s *SpecStore) LoadName(name string) ([]byte, error) {
	if s == nil || s.Dir == "" {
		return nil, errors.New("spec store is not configured")
	}
	index, err := s.specIndex()
	if err != nil {
		return nil, err
	}
	id, ok := index[name]
	if !ok {
		return nil, fmt.Errorf("spec %q not found", name)
	}
	return s.loadID(id)
}

func (s *SpecStore) specIndex() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.index, nil
	}
	var data []byte
	for _, name := range []string{"index.bin", "index.json"} {
		raw, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if strings.HasSuffix(name, ".bin") {
			raw, err = Decrypt(raw)
			if err != nil {
				return nil, fmt.Errorf("decrypt spec index: %w", err)
			}
			raw, err = decompress(raw)
			if err != nil {
				return nil, fmt.Errorf("decompress spec index: %w", err)
			}
		}
		data = raw
		break
	}
	if data == nil {
		return nil, errors.New("spec index not found")
	}
	var index map[string]string
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse spec index: %w", err)
	}
	s.index = index
	s.loaded = true
	return index, nil
}

func (s *SpecStore) loadID(id string) ([]byte, error) {
	candidates := []string{
		filepath.Join(s.Dir, id+".bin"),
		filepath.Join(s.Dir, id+".js"),
	}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read spec %q: %w", id, err)
		}
		if len(data) > maxEncryptedBytes {
			return nil, fmt.Errorf("spec %q exceeds size limit", id)
		}
		if strings.HasSuffix(candidate, ".bin") {
			data, err = Decrypt(data)
			if err != nil {
				return nil, err
			}
			return decompress(data)
		}
		return data, nil
	}
	return nil, fmt.Errorf("spec %q not found", id)
}

func validSpecID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func decompress(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		return data, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	plain, err := io.ReadAll(io.LimitReader(reader, maxEncryptedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(plain) > maxEncryptedBytes {
		return nil, errors.New("decompressed spec exceeds size limit")
	}
	return plain, nil
}
