package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// dataDirEnv overrides the per-user data directory, matching the variable the
// Docker image uses for its data volume.
const dataDirEnv = "FE_DATA_DIR"

// desktopPaths are the per-user locations of the desktop's state. config.yaml
// and the SQLite database live together in DataDir.
type desktopPaths struct {
	DataDir    string
	ConfigFile string
	DBFile     string
	LogFile    string
}

// resolveDesktopPaths picks the data directory: the explicit override (flag,
// then FE_DATA_DIR), else the platform default.
func resolveDesktopPaths(override string, getenv func(string) string, goos string) (desktopPaths, error) {
	dir := override
	if dir == "" {
		dir = getenv(dataDirEnv)
	}
	if dir == "" {
		var err error
		if dir, err = defaultDataDir(getenv, goos); err != nil {
			return desktopPaths{}, err
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return desktopPaths{}, fmt.Errorf("resolve data directory %q: %w", dir, err)
	}
	return desktopPaths{
		DataDir:    abs,
		ConfigFile: filepath.Join(abs, "config.yaml"),
		DBFile:     filepath.Join(abs, "fileengine.db"),
		LogFile:    filepath.Join(abs, "logs", "fileengine-desktop.log"),
	}, nil
}

// defaultDataDir follows each platform's convention for per-user app data:
// %LOCALAPPDATA%\FileEngine, ~/Library/Application Support/FileEngine, and
// $XDG_DATA_HOME/fileengine (default ~/.local/share/fileengine) elsewhere.
func defaultDataDir(getenv func(string) string, goos string) (string, error) {
	switch goos {
	case "windows":
		if base := getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "FileEngine"), nil
		}
		return "", errors.New("LOCALAPPDATA is not set; pass --data-dir or set " + dataDirEnv)
	case "darwin":
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("HOME is not set; pass --data-dir or set " + dataDirEnv)
		}
		return filepath.Join(home, "Library", "Application Support", "FileEngine"), nil
	default:
		if base := getenv("XDG_DATA_HOME"); filepath.IsAbs(base) {
			return filepath.Join(base, "fileengine"), nil
		}
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("HOME is not set; pass --data-dir or set " + dataDirEnv)
		}
		return filepath.Join(home, ".local", "share", "fileengine"), nil
	}
}

// prepare creates the directories and, on first launch, a config.yaml that
// points the SQLite database into the data directory. Everything else falls
// back to the config package defaults.
func (p desktopPaths) prepare() error {
	if err := os.MkdirAll(filepath.Dir(p.LogFile), 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if _, err := os.Stat(p.ConfigFile); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", p.ConfigFile, err)
	}
	initial := map[string]any{
		"database": map[string]string{"driver": "sqlite", "dsn": p.DBFile},
	}
	data, err := yaml.Marshal(initial)
	if err != nil {
		return err
	}
	// 0600: the config later holds model provider API keys.
	if err := os.WriteFile(p.ConfigFile, data, 0o600); err != nil {
		return fmt.Errorf("write initial config: %w", err)
	}
	return nil
}
