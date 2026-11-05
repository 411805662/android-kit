package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Paths holds every directory the tool owns. All of them can be overridden by
// environment variables so a user can relocate any part of the installation.
type Paths struct {
	Home        string `json:"home"`
	Binary      string `json:"binary"`
	Shim        string `json:"shim"`
	BinDir      string `json:"bin_dir"`
	Root        string `json:"root"`
	ConfigDir   string `json:"config_dir"`
	DataDir     string `json:"data_dir"`
	StateDir    string `json:"state_dir"`
	CacheDir    string `json:"cache_dir"`
	BackupDir   string `json:"backup_dir"`
	ProjectsDir string `json:"projects_dir"`
	Manifest    string `json:"manifest"`
	WebToken    string `json:"web_token"`
	WebURL      string `json:"web_url"`
	UITheme     string `json:"ui_theme"`
	Stamp       string `json:"stamp"`
	LogsDir     string `json:"logs_dir"`
	Artifacts   string `json:"artifacts"`
}

func envOr(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return expandHome(v)
		}
	}
	return ""
}

func expandHome(p string) string {
	if p == "~" || len(p) > 1 && p[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func ResolvePaths() Paths {
	home, _ := os.UserHomeDir()
	p := Paths{Home: home}
	root := filepath.Join(home, ".android-kit")

	if customRoot := envOr("ANDROID_KIT_HOME", "AK_HOME"); customRoot != "" {
		root = customRoot
		p.ConfigDir = root
		p.DataDir = root
		p.StateDir = root
		p.CacheDir = filepath.Join(root, "cache")
		p.BinDir = filepath.Join(root, "bin")
	} else if runtime.GOOS == "windows" {
		appData := envOr("APPDATA")
		local := envOr("LOCALAPPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		root = filepath.Join(appData, "."+appName)
		p.ConfigDir = root
		p.DataDir = root
		p.StateDir = root
		p.CacheDir = filepath.Join(root, "cache")
		p.BinDir = filepath.Join(local, "Programs", appName)
	} else {
		p.ConfigDir = root
		p.DataDir = root
		p.StateDir = root
		p.CacheDir = filepath.Join(root, "cache")
		p.BinDir = filepath.Join(home, ".local", "bin")
	}

	p.Root = root
	if v := envOr("ANDROID_KIT_CONFIG_DIR", "AK_CONFIG_DIR"); v != "" {
		p.ConfigDir = v
	}
	if v := envOr("ANDROID_KIT_DATA_DIR", "AK_DATA_DIR"); v != "" {
		p.DataDir = v
	}
	if v := envOr("ANDROID_KIT_STATE_DIR", "AK_STATE_DIR"); v != "" {
		p.StateDir = v
	}
	if v := envOr("ANDROID_KIT_CACHE_DIR", "AK_CACHE_DIR"); v != "" {
		p.CacheDir = v
	}
	if v := envOr("ANDROID_KIT_BIN_DIR", "AK_BIN_DIR"); v != "" {
		p.BinDir = v
	}

	p.ProjectsDir = filepath.Join(p.ConfigDir, "projects")
	p.BackupDir = filepath.Join(p.DataDir, "backups")
	p.Manifest = filepath.Join(p.DataDir, "install-manifest.json")
	p.WebToken = filepath.Join(p.StateDir, "web.token")
	p.WebURL = filepath.Join(p.StateDir, "web.url")
	p.UITheme = filepath.Join(p.StateDir, "ui-theme")
	p.Stamp = filepath.Join(p.StateDir, "reload.stamp")
	p.LogsDir = filepath.Join(p.StateDir, "logs")
	p.Artifacts = filepath.Join(p.DataDir, "artifacts")

	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		p.Binary = exe
	}
	if v := envOr("ANDROID_KIT_BINARY"); v != "" {
		p.Binary = v
	}
	if p.Binary == "" {
		p.Binary = filepath.Join(p.BinDir, appName)
	}
	p.Shim = filepath.Join(p.BinDir, "ak")
	return p
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.Root, p.ConfigDir, p.DataDir, p.StateDir, p.CacheDir, p.BackupDir, p.ProjectsDir, p.LogsDir, p.Artifacts, filepath.Join(p.Artifacts, "ui")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), mode)
}
