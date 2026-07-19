// Package authfile provides crash-safe native credential-file updates.
package authfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func Read(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return v, nil
}

func AtomicWrite(path string, v map[string]any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err = d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	if err = d.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
func String(m map[string]any, key string) string         { s, _ := m[key].(string); return s }
func Object(m map[string]any, key string) map[string]any { x, _ := m[key].(map[string]any); return x }
