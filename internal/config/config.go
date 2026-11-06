// Package config persists user settings and manual rules in
// ~/.config/adbl/config.json (path injectable for tests).
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	// EnabledSources maps source id -> enabled. Missing id = enabled (new
	// default sources become active without a config migration).
	EnabledSources map[string]bool `json:"enabled_sources,omitempty"`
	// Manual rules; allow always wins over any blocklist.
	Allow []string `json:"allow,omitempty"`
	Block []string `json:"block,omitempty"`
}

type File struct {
	path string
	mu   sync.Mutex
	cfg  Config
}

// Open loads the config at path, or creates it with defaults when absent.
func Open(path string) (*File, error) {
	f := &File{path: path, cfg: Config{EnabledSources: map[string]bool{}}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return f, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &f.cfg); err != nil {
		return nil, err
	}
	if f.cfg.EnabledSources == nil {
		f.cfg.EnabledSources = map[string]bool{}
	}
	return f, nil
}

// Get returns a copy of the current config.
func (f *File) Get() Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := Config{
		EnabledSources: make(map[string]bool, len(f.cfg.EnabledSources)),
		Allow:          append([]string(nil), f.cfg.Allow...),
		Block:          append([]string(nil), f.cfg.Block...),
	}
	for k, v := range f.cfg.EnabledSources {
		out.EnabledSources[k] = v
	}
	return out
}

// Update applies fn to the config and persists it atomically (tmp + rename).
func (f *File) Update(fn func(*Config)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.cfg)
	return f.save()
}

func (f *File) save() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(&f.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
