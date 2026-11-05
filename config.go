package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type AliasesConfig struct {
	Preset string            `toml:"preset" json:"preset"`
	Force  bool              `toml:"force" json:"force"`
	Items  map[string]string `toml:"items" json:"items"`
}

type UIConfig struct {
	Theme           string `toml:"theme" json:"theme"`
	OpenBrowser     bool   `toml:"open_browser" json:"open_browser"`
	AllowRunFromWeb bool   `toml:"allow_run_from_web" json:"allow_run_from_web"`
}

type PathsConfig struct {
	ArtifactsMode string `toml:"artifacts_mode" json:"artifacts_mode"`
	HiddenDir     string `toml:"hidden_dir" json:"hidden_dir"`
	VisibleDir    string `toml:"visible_dir" json:"visible_dir"`
}

type AndroidCLIConfig struct {
	Path       string `toml:"path" json:"path"`
	MinVersion string `toml:"min_version" json:"min_version"`
}

type Config struct {
	Version        int              `toml:"version" json:"version"`
	DefaultProject string           `toml:"default_project" json:"default_project"`
	Prefer         string           `toml:"prefer" json:"prefer"`
	Language       string           `toml:"language" json:"language"`
	Aliases        AliasesConfig    `toml:"aliases" json:"aliases"`
	UI             UIConfig         `toml:"ui" json:"ui"`
	Paths          PathsConfig      `toml:"paths" json:"paths"`
	AndroidCLI     AndroidCLIConfig `toml:"android_cli" json:"android_cli"`
}

func classicAliases() map[string]string {
	return map[string]string{
		"b":   "build",
		"cb":  "build --clean",
		"in":  "install",
		"ba":  "run",
		"bio": "run",
		"k":   "stop",
		"o":   "open",
		"ko":  "restart",
		"c":   "clear",
		"un":  "app-uninstall",
		"ss":  "screenshot",
		"ui":  "layout -p",
		"l":   "logcat",
		"lc":  "logcat -c",
		"dev": "cd",
	}
}

func DefaultConfig() Config {
	return Config{
		Version:        2,
		DefaultProject: "",
		Prefer:         "auto",
		Language:       "zh-CN",
		Aliases: AliasesConfig{
			Preset: "classic",
			Force:  false,
			Items:  classicAliases(),
		},
		UI: UIConfig{
			Theme:           "dark",
			OpenBrowser:     true,
			AllowRunFromWeb: true,
		},
		Paths: PathsConfig{
			ArtifactsMode: "project",
			HiddenDir:     ".android-kit",
			VisibleDir:    "android-kit",
		},
		AndroidCLI: AndroidCLIConfig{MinVersion: "1.1.0"},
	}
}

func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

func LoadConfig(p Paths) Config {
	cfg := DefaultConfig()
	path := p.ConfigFile()
	if !exists(path) {
		return cfg
	}
	var raw Config
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot parse %s: %v\n", path, err)
		return cfg
	}
	if raw.Version != 0 {
		cfg.Version = raw.Version
	}
	cfg.DefaultProject = raw.DefaultProject
	if raw.Prefer != "" {
		cfg.Prefer = raw.Prefer
	}
	if raw.Language != "" {
		cfg.Language = raw.Language
	}
	if raw.Aliases.Preset != "" {
		cfg.Aliases.Preset = raw.Aliases.Preset
	}
	cfg.Aliases.Force = raw.Aliases.Force
	if raw.Aliases.Items != nil {
		cfg.Aliases.Items = raw.Aliases.Items
	}
	if cfg.Aliases.Preset == "classic" && len(cfg.Aliases.Items) == 0 {
		cfg.Aliases.Items = classicAliases()
	}
	cfg.UI = raw.UI
	if cfg.UI.Theme == "" {
		cfg.UI.Theme = "dark"
	}
	// v1 shipped with allow_run_from_web=false; v2 turns it on by default.
	if raw.Version < 2 {
		cfg.UI.AllowRunFromWeb = true
	}
	cfg.Paths = raw.Paths
	if cfg.Paths.ArtifactsMode == "" {
		cfg.Paths.ArtifactsMode = "project"
	}
	if cfg.Paths.HiddenDir == "" {
		cfg.Paths.HiddenDir = ".android-kit"
	}
	if cfg.Paths.VisibleDir == "" {
		cfg.Paths.VisibleDir = "android-kit"
	}
	cfg.AndroidCLI = raw.AndroidCLI
	if cfg.AndroidCLI.MinVersion == "" {
		cfg.AndroidCLI.MinVersion = "1.1.0"
	}
	return cfg
}

func SaveConfig(p Paths, cfg Config) error {
	var b strings.Builder
	b.WriteString("# android-kit configuration\n")
	b.WriteString("# Docs: https://github.com/411805662/android-kit\n\n")
	fmt.Fprintf(&b, "version = %d\n", 2)
	fmt.Fprintf(&b, "default_project = %s\n", strconv.Quote(cfg.DefaultProject))
	fmt.Fprintf(&b, "prefer = %s\n", strconv.Quote(cfg.Prefer))
	fmt.Fprintf(&b, "language = %s\n\n", strconv.Quote(cfg.Language))

	b.WriteString("[aliases]\n")
	fmt.Fprintf(&b, "preset = %s\n", strconv.Quote(cfg.Aliases.Preset))
	fmt.Fprintf(&b, "force = %t\n\n", cfg.Aliases.Force)
	b.WriteString("[aliases.items]\n")
	names := make([]string, 0, len(cfg.Aliases.Items))
	for name := range cfg.Aliases.Items {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "%s = %s\n", tomlKey(name), strconv.Quote(cfg.Aliases.Items[name]))
	}

	b.WriteString("\n[ui]\n")
	fmt.Fprintf(&b, "theme = %s\n", strconv.Quote(cfg.UI.Theme))
	fmt.Fprintf(&b, "open_browser = %t\n", cfg.UI.OpenBrowser)
	fmt.Fprintf(&b, "allow_run_from_web = %t\n\n", cfg.UI.AllowRunFromWeb)

	b.WriteString("[paths]\n")
	fmt.Fprintf(&b, "artifacts_mode = %s\n", strconv.Quote(cfg.Paths.ArtifactsMode))
	fmt.Fprintf(&b, "hidden_dir = %s\n", strconv.Quote(cfg.Paths.HiddenDir))
	fmt.Fprintf(&b, "visible_dir = %s\n\n", strconv.Quote(cfg.Paths.VisibleDir))

	b.WriteString("[android_cli]\n")
	fmt.Fprintf(&b, "path = %s\n", strconv.Quote(cfg.AndroidCLI.Path))
	fmt.Fprintf(&b, "min_version = %s\n", strconv.Quote(cfg.AndroidCLI.MinVersion))

	if err := atomicWrite(p.ConfigFile(), []byte(b.String()), 0o600); err != nil {
		return err
	}
	TouchStamp(p)
	return nil
}

func tomlKey(k string) string {
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return strconv.Quote(k)
		}
	}
	return k
}

func TouchStamp(p Paths) {
	_ = os.MkdirAll(p.StateDir, 0o755)
	_ = atomicWrite(p.Stamp, []byte(fmt.Sprintf("%d\n", timeNowNano())), 0o644)
}

func Stamp(p Paths) string {
	data, err := os.ReadFile(p.Stamp)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
