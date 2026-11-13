package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"offline-complete/internal/settings"
)

const backupSchemaVersion = 1

type backupManifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	CreatedAt     time.Time    `json:"createdAt"`
	Files         []backupFile `json:"files"`
}

type backupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func backupCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: oc-manage backup export|import [--file PATH]")
		return 2
	}
	path := ""
	if len(args) > 1 {
		if len(args) != 3 || args[1] != "--file" {
			fmt.Fprintln(stderr, "usage: oc-manage backup export|import [--file PATH]")
			return 2
		}
		path = args[2]
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "oc-manage: %v\n", err)
			return 1
		}
		path = filepath.Join(home, ".oc", "offline-complete-backup.zip")
	}
	switch args[0] {
	case "export":
		if err := exportBackup(path); err != nil {
			fmt.Fprintf(stderr, "oc-manage: backup export: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "backup: %s\n", path)
	case "import":
		if err := importBackup(path); err != nil {
			fmt.Fprintf(stderr, "oc-manage: backup import: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "restored: %s\n", path)
	default:
		fmt.Fprintln(stderr, "usage: oc-manage backup export|import [--file PATH]")
		return 2
	}
	return 0
}

func exportBackup(path string) error {
	local, err := openStore()
	if err != nil {
		return err
	}
	learning, err := local.Export()
	if err != nil {
		return err
	}
	cfg, err := settings.Load()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	config, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	config = append(config, '\n')

	files := map[string][]byte{
		"learning.json": learning,
		"config.json":   config,
	}
	manifest := backupManifest{SchemaVersion: backupSchemaVersion, CreatedAt: time.Now().UTC()}
	for _, name := range []string{"config.json", "learning.json"} {
		manifest.Files = append(manifest.Files, backupFile{
			Name:   name,
			Size:   int64(len(files[name])),
			SHA256: digest(files[name]),
		})
	}
	readme := []byte("Offline Complete 本地备份\n\n包含学习数据和个人纠正 (learning.json)、设置 (config.json)。\n不包含日志、规则包、终端内容或运行中的 socket。\nconfig.json 可能包含 AI API Key，请按敏感文件保管。\n")
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if err := writeZipEntry(writer, "manifest.json", manifestData); err != nil {
		return err
	}
	for _, name := range []string{"learning.json", "config.json"} {
		if err := writeZipEntry(writer, name, files[name]); err != nil {
			return err
		}
	}
	if err := writeZipEntry(writer, "README.txt", readme); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return writeBackupFile(path, buffer.Bytes())
}

func importBackup(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()

	entries := map[string][]byte{}
	var manifest backupManifest
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(file.Name)
		if name != file.Name || (name != "manifest.json" && name != "learning.json" && name != "config.json" && name != "README.txt") {
			return fmt.Errorf("unexpected archive entry %q", file.Name)
		}
		if _, exists := entries[name]; exists {
			return fmt.Errorf("duplicate archive entry %q", name)
		}
		if file.UncompressedSize64 > 16<<20 {
			return fmt.Errorf("archive entry %q exceeds size limit", name)
		}
		handle, err := file.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(handle, 16<<20+1))
		closeErr := handle.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > 16<<20 {
			return fmt.Errorf("archive entry %q exceeds size limit", name)
		}
		entries[name] = data
	}
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if manifest.SchemaVersion != backupSchemaVersion {
		return fmt.Errorf("unsupported backup schema: %d", manifest.SchemaVersion)
	}
	for _, item := range manifest.Files {
		data, ok := entries[item.Name]
		if !ok {
			return fmt.Errorf("backup is missing %s", item.Name)
		}
		if int64(len(data)) != item.Size || digest(data) != item.SHA256 {
			return fmt.Errorf("backup entry %s failed integrity check", item.Name)
		}
	}
	if entries["learning.json"] == nil {
		return errors.New("backup is missing learning.json")
	}

	// Validate settings before touching either live file.
	cfg := settings.Config{MenuBar: settings.MenuBar{Enabled: true}}
	if data := entries["config.json"]; data != nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse settings: %w", err)
		}
		if err := cfg.Normalize(); err != nil {
			return fmt.Errorf("invalid settings: %w", err)
		}
	}

	local, err := openStore()
	if err != nil {
		return err
	}
	learningPath := local.Stats().Path
	configPath, err := settings.ConfigPath()
	if err != nil {
		return err
	}
	learningBefore, learningExisted, err := readOptional(learningPath)
	if err != nil {
		return err
	}
	configBefore, configExisted, err := readOptional(configPath)
	if err != nil {
		return err
	}
	if learningExisted {
		if err := writeBackupFile(learningPath+".bak", learningBefore); err != nil {
			return fmt.Errorf("backup learning before restore: %w", err)
		}
	}
	if configExisted {
		if err := writeBackupFile(configPath+".bak", configBefore); err != nil {
			return fmt.Errorf("backup settings before restore: %w", err)
		}
	}
	restore := func() {
		_ = restoreOptional(learningPath, learningBefore, learningExisted)
		_ = restoreOptional(configPath, configBefore, configExisted)
	}

	if err := local.Import(entries["learning.json"]); err != nil {
		return fmt.Errorf("import learning data: %w", err)
	}
	if entries["config.json"] != nil {
		if err := settings.Save(cfg); err != nil {
			restore()
			return fmt.Errorf("import settings: %w", err)
		}
	}
	return nil
}

func writeZipEntry(writer *zip.Writer, name string, data []byte) error {
	entry, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readOptional(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func restoreOptional(path string, data []byte, existed bool) error {
	if !existed {
		return os.Remove(path)
	}
	return writeBackupFile(path, data)
}

func writeBackupFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".offline-complete-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
