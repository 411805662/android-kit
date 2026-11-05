package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RCEdit struct {
	Path   string `json:"path"`
	Backup string `json:"backup,omitempty"`
}

type Manifest struct {
	Version     string            `json:"version"`
	InstalledAt string            `json:"installed_at"`
	Binary      string            `json:"binary"`
	Shim        string            `json:"shim"`
	RCEdits     []RCEdit          `json:"rc_edits"`
	Dirs        map[string]string `json:"dirs"`
}

func (a *App) readManifest() *Manifest {
	data, err := os.ReadFile(a.Paths.Manifest)
	if err != nil {
		return nil
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return &m
}

func (a *App) writeManifest(backups map[string]string) error {
	m := &Manifest{
		Version:     appVersion,
		InstalledAt: time.Now().Format(time.RFC3339),
		Binary:      a.Paths.Binary,
		Shim:        a.Paths.Shim,
		Dirs: map[string]string{
			"config": a.Paths.ConfigDir,
			"data":   a.Paths.DataDir,
			"state":  a.Paths.StateDir,
			"cache":  a.Paths.CacheDir,
			"bin":    a.Paths.BinDir,
		},
	}
	for _, rc := range a.rcFiles() {
		data, err := os.ReadFile(rc)
		if err == nil && strings.Contains(string(data), shellBlockStart) {
			m.RCEdits = append(m.RCEdits, RCEdit{Path: rc, Backup: backups[rc]})
		}
	}
	return writeJSON(a.Paths.Manifest, m, 0o600)
}

func (a *App) cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "非交互")
	shell := fs.String("shell", "auto", "zsh|bash|auto")
	aliases := fs.String("aliases", "", "classic|none")
	forceAliases := fs.Bool("force-aliases", false, "覆盖同名 alias/function")
	noShell := fs.Bool("no-shell", false, "不修改 shell rc")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = yes
	_ = shell
	if err := a.Paths.Ensure(); err != nil {
		return err
	}
	cfg := LoadConfig(a.Paths)
	if !exists(a.Paths.ConfigFile()) {
		cfg = DefaultConfig()
	}
	if *aliases != "" {
		switch *aliases {
		case "classic":
			cfg.Aliases.Preset = "classic"
			cfg.Aliases.Items = classicAliases()
		case "none":
			cfg.Aliases.Preset = "none"
			cfg.Aliases.Items = map[string]string{}
		default:
			return fmt.Errorf("--aliases 只支持 classic 或 none")
		}
	}
	if *forceAliases {
		cfg.Aliases.Force = true
	}
	if err := SaveConfig(a.Paths, cfg); err != nil {
		return err
	}
	if !*noShell {
		backups, err := a.installShellBlock()
		if err != nil {
			return err
		}
		if err := a.writeManifest(backups); err != nil {
			return err
		}
	} else {
		if err := a.writeManifest(nil); err != nil {
			return err
		}
	}
	fmt.Println()
	fmt.Printf("android-kit %s installed\n", appVersion)
	fmt.Printf("  binary:  %s\n", a.Paths.Binary)
	fmt.Printf("  config:  %s\n", a.Paths.ConfigFile())
	fmt.Printf("  aliases: %s\n", cfg.Aliases.Preset)
	fmt.Println()
	fmt.Println("下一步:")
	fmt.Println("  ak project add /path/to/your/android/project --default")
	fmt.Println("  ak doctor")
	fmt.Println("  ak web")
	fmt.Println()
	fmt.Println("当前终端立即生效:  eval \"$(android-kit shell init --quiet)\"")
	return nil
}

func (a *App) cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "同时删除配置和项目列表")
	dryRun := fs.Bool("dry-run", false, "只打印，不执行")
	yes := fs.Bool("yes", false, "不询问")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = yes
	m := a.readManifest()
	if m == nil {
		m = &Manifest{Binary: filepath.Join(a.Paths.BinDir, appName), Shim: a.Paths.Shim}
	}
	if !*dryRun && !*yes {
		fmt.Println("将删除: shell 集成块、二进制、缓存、状态、安装清单")
		if *purge {
			fmt.Println("并删除: 配置目录（含所有项目配置）")
		}
		if !confirm("继续卸载 android-kit?") {
			fmt.Println("已取消")
			return nil
		}
	}
	if err := a.removeShellBlock(*dryRun); err != nil {
		return err
	}
	remove := func(path string) {
		if path == "" || !exists(path) {
			return
		}
		if *dryRun {
			fmt.Printf("would remove %s\n", path)
			return
		}
		if err := os.RemoveAll(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", path, err)
		} else {
			fmt.Printf("removed %s\n", path)
		}
	}
	remove(m.Shim)
	if m.Binary != "" && m.Binary != a.Paths.Binary {
		remove(m.Binary)
	} else if m.Binary != "" {
		remove(m.Binary)
	}
	remove(a.Paths.CacheDir)
	remove(a.Paths.StateDir)
	remove(a.Paths.DataDir)
	if *purge {
		remove(a.Paths.ConfigDir)
	}
	if !*purge {
		fmt.Printf("kept config: %s\n", a.Paths.ConfigDir)
	}
	if *dryRun {
		fmt.Println("dry-run complete; nothing changed")
	} else {
		fmt.Println("android-kit uninstalled")
	}
	return nil
}
