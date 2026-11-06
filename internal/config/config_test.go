package config

import (
	"path/filepath"
	"testing"
)

func TestOpenDefaultsWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.Get()
	if len(cfg.Allow) != 0 || len(cfg.Block) != 0 || len(cfg.EnabledSources) != 0 {
		t.Fatalf("expected defaults, got %+v", cfg)
	}
}

func TestUpdatePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Update(func(c *Config) {
		c.Allow = append(c.Allow, "good.example.com")
		c.EnabledSources["src1"] = false
	}); err != nil {
		t.Fatal(err)
	}

	f2, err := Open(path) // simulate restart
	if err != nil {
		t.Fatal(err)
	}
	cfg := f2.Get()
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "good.example.com" {
		t.Fatalf("allow not persisted: %+v", cfg.Allow)
	}
	if cfg.EnabledSources["src1"] != false {
		t.Fatalf("enabled flag not persisted: %+v", cfg.EnabledSources)
	}
}

func TestUpdateKeepsCopyIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	f, _ := Open(path)
	if err := f.Update(func(c *Config) { c.Block = append(c.Block, "a.example.com") }); err != nil {
		t.Fatal(err)
	}
	got := f.Get()
	got.Block[0] = "tampered" // caller mutates its copy
	if f.Get().Block[0] != "a.example.com" {
		t.Fatal("Get must return an isolated copy")
	}
}
