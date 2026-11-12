// Command encrypt-rules builds the encrypted resources shipped in the app.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"offline-complete/internal/rules"
)

func main() {
	if len(os.Args) != 3 && len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: encrypt-rules BASE_JSON BASE_BIN [SPECS_DIR SPECS_OUT]")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	var pack struct {
		Specs map[string]string `json:"specs"`
	}
	if err := json.Unmarshal(data, &pack); err != nil {
		fatal(err)
	}
	encrypted, err := rules.Encrypt(data)
	if err != nil {
		fatal(err)
	}
	if err := write(os.Args[2], encrypted); err != nil {
		fatal(err)
	}
	if len(os.Args) == 5 {
		outputDir := os.Args[4]
		if err := os.RemoveAll(outputDir); err != nil {
			fatal(err)
		}
		if err := os.MkdirAll(outputDir, 0o700); err != nil {
			fatal(err)
		}
		index := map[string]string{}
		indexPath := filepath.Join(os.Args[3], "index.json")
		if indexData, err := os.ReadFile(indexPath); err == nil {
			if err := json.Unmarshal(indexData, &index); err != nil {
				fatal(fmt.Errorf("parse spec index: %w", err))
			}
		} else {
			for name, id := range pack.Specs {
				index[name] = id
			}
		}
		seen := make(map[string]bool)
		for _, id := range index {
			if seen[id] {
				continue
			}
			seen[id] = true
			source, err := os.ReadFile(filepath.Join(os.Args[3], id+".js"))
			if err != nil {
				fatal(fmt.Errorf("spec %s: %w", id, err))
			}
			sealed, err := rules.Encrypt(gzipData(source))
			if err != nil {
				fatal(err)
			}
			if err := write(filepath.Join(outputDir, id+".bin"), sealed); err != nil {
				fatal(err)
			}
		}
		indexData, err := json.Marshal(index)
		if err != nil {
			fatal(err)
		}
		sealedIndex, err := rules.Encrypt(gzipData(indexData))
		if err != nil {
			fatal(err)
		}
		if err := write(filepath.Join(outputDir, "index.bin"), sealedIndex); err != nil {
			fatal(err)
		}
		fmt.Printf("encrypted rules=%s specs=%d names=%d\n", os.Args[2], len(seen), len(index))
		return
	}
	fmt.Printf("encrypted rules=%s\n", os.Args[2])
}

func write(path string, data []byte) error {
	if path == "" {
		return errors.New("empty output path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "encrypt-rules:", err)
	os.Exit(1)
}

func gzipData(data []byte) []byte {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, _ = writer.Write(data)
	_ = writer.Close()
	return buffer.Bytes()
}
