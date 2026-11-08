package main

import (
	"os"
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaultDataDir(t *testing.T) {
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}, filepath.Join(`C:\Users\u\AppData\Local`, "FileEngine")},
		{"darwin", map[string]string{"HOME": "/Users/u"}, "/Users/u/Library/Application Support/FileEngine"},
		{"linux", map[string]string{"HOME": "/home/u"}, "/home/u/.local/share/fileengine"},
		{"linux", map[string]string{"HOME": "/home/u", "XDG_DATA_HOME": "/xdg"}, "/xdg/fileengine"},
		{"linux", map[string]string{"HOME": "/home/u", "XDG_DATA_HOME": "relative"}, "/home/u/.local/share/fileengine"},
	}
	for _, c := range cases {
		got, err := defaultDataDir(envOf(c.env), c.goos)
		if err != nil || got != filepath.FromSlash(c.want) && got != c.want {
			t.Errorf("%s %v = %q, %v; want %q", c.goos, c.env, got, err, c.want)
		}
	}
	if _, err := defaultDataDir(envOf(nil), "windows"); err == nil {
		t.Error("windows without LOCALAPPDATA must fail")
	}
}

func TestResolveDesktopPathsOverrides(t *testing.T) {
	flagDir, envDir := t.TempDir(), t.TempDir()
	env := envOf(map[string]string{dataDirEnv: envDir, "HOME": "/home/u"})
	p, err := resolveDesktopPaths(flagDir, env, "linux")
	if err != nil || p.DataDir != flagDir {
		t.Fatalf("flag override: %+v %v", p, err)
	}
	p, err = resolveDesktopPaths("", env, "linux")
	if err != nil || p.DataDir != envDir {
		t.Fatalf("env override: %+v %v", p, err)
	}
}

func TestPrepareWritesLoadableConfigOnce(t *testing.T) {
	p, err := resolveDesktopPaths(t.TempDir(), envOf(nil), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.prepare(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Driver != "sqlite" || cfg.Database.DSN != p.DBFile {
		t.Fatalf("unexpected database config %+v", cfg.Database)
	}

	if err := os.WriteFile(p.ConfigFile, []byte("database:\n  driver: mysql\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.prepare(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p.ConfigFile)
	if string(data) != "database:\n  driver: mysql\n" {
		t.Fatal("prepare must never overwrite an existing config")
	}
}
