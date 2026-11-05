package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (a *App) cmdProject(args []string) error {
	if len(args) == 0 {
		return a.listProjects()
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		return a.listProjects()
	case "add":
		return a.addProject(rest)
	case "show":
		return a.showProject(rest)
	case "use", "default":
		return a.cmdUse(rest)
	case "remove", "rm", "delete":
		return a.removeProject(rest)
	case "detect":
		return a.detectProject(rest)
	default:
		return fmt.Errorf("未知子命令: ak project %s", sub)
	}
}

func (a *App) listProjects() error {
	projects := a.Store.List()
	if a.Opts.JSON {
		return printJSON(projects)
	}
	if len(projects) == 0 {
		fmt.Println("还没有登记项目。运行: ak project add /path/to/android/project")
		return nil
	}
	fmt.Printf("%-2s %-18s %-24s %-38s %s\n", "", "ID", "NAME", "PACKAGE", "ROOT")
	for _, p := range projects {
		mark := " "
		if p.ID == a.Config.DefaultProject {
			mark = "*"
		}
		fmt.Printf("%-2s %-18s %-24s %-38s %s\n", mark, p.ID, truncate(p.Name, 24), truncate(p.Package, 38), p.Root)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func (a *App) addProject(args []string) error {
	fs := flag.NewFlagSet("project add", flag.ContinueOnError)
	id := fs.String("id", "", "项目 ID")
	name := fs.String("name", "", "显示名称")
	pkg := fs.String("package", "", "应用包名")
	activity := fs.String("activity", "", "启动 Activity")
	module := fs.String("module", "", "Gradle 模块，例如 :app")
	variant := fs.String("variant", "", "构建变体，默认 debug")
	apkGlob := fs.String("apk-glob", "", "APK 相对路径 glob")
	device := fs.String("device", "", "默认设备 serial")
	makeDefault := fs.Bool("default", false, "设为默认项目")
	noDetect := fs.Bool("no-detect", false, "不做自动探测")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := "."
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	path = expandHome(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !isDir(abs) {
		return fmt.Errorf("目录不存在: %s", abs)
	}
	var proj *Project
	if !*noDetect {
		proj, err = DetectProject(abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: 自动探测失败: %v\n", err)
			proj = &Project{Name: filepath.Base(abs), Root: abs, Module: ":app", Variant: "debug", Env: map[string]string{}}
		}
	} else {
		proj = &Project{Name: filepath.Base(abs), Root: abs, Module: ":app", Variant: "debug", Env: map[string]string{}}
	}
	proj.Root = abs
	if *id != "" {
		proj.ID = *id
	}
	if *name != "" {
		proj.Name = *name
	}
	if *pkg != "" {
		proj.Package = *pkg
	}
	if *activity != "" {
		proj.Activity = *activity
	}
	if *module != "" {
		proj.Module = *module
	}
	if *variant != "" {
		proj.Variant = *variant
	}
	if *apkGlob != "" {
		proj.APKGlob = *apkGlob
	}
	if *device != "" {
		proj.Device = *device
	}
	if proj.APKGlob == "" {
		proj.APKGlob = apkGlobFor(proj.Module, proj.Variant)
	}
	if proj.ID == "" {
		proj.ID = slug(proj.Name)
	}
	existed := a.Store.Get(proj.ID) != nil
	if err := a.Store.Save(proj); err != nil {
		return err
	}
	if *makeDefault || a.Config.DefaultProject == "" {
		if err := a.Store.SetDefault(proj.ID); err != nil {
			return err
		}
	}
	a.reload()
	if a.Opts.JSON {
		return printJSON(proj)
	}
	verb := "added"
	if existed {
		verb = "updated"
	}
	fmt.Printf("%s project %s\n", verb, proj.ID)
	fmt.Printf("  root:     %s\n", proj.Root)
	fmt.Printf("  package:  %s\n", orDash(proj.Package))
	fmt.Printf("  activity: %s\n", orDash(proj.Activity))
	fmt.Printf("  module:   %s (%s)\n", proj.Module, proj.Variant)
	fmt.Printf("  apk_glob: %s\n", proj.APKGlob)
	if a.Config.DefaultProject == proj.ID {
		fmt.Printf("  default:  yes\n")
	}
	return nil
}

func (a *App) showProject(args []string) error {
	id := a.Opts.Project
	if len(args) > 0 {
		id = args[0]
	}
	proj := a.Store.Get(id)
	if proj == nil {
		var err error
		proj, err = a.project(false)
		if err != nil || proj == nil {
			return fmt.Errorf("项目不存在: %s", id)
		}
	}
	if a.Opts.JSON {
		return printJSON(proj)
	}
	fmt.Printf("id = %q\n", proj.ID)
	fmt.Printf("name = %q\n", proj.Name)
	fmt.Printf("root = %q\n", proj.Root)
	fmt.Printf("package = %q\n", proj.Package)
	fmt.Printf("activity = %q\n", proj.Activity)
	fmt.Printf("module = %q\n", proj.Module)
	fmt.Printf("variant = %q\n", proj.Variant)
	fmt.Printf("apk_glob = %q\n", proj.APKGlob)
	fmt.Printf("device = %q\n", proj.Device)
	if len(proj.Env) > 0 {
		fmt.Println("\n[env]")
		keys := make([]string, 0, len(proj.Env))
		for k := range proj.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s = %q\n", k, proj.Env[k])
		}
	}
	return nil
}

func (a *App) cmdUse(args []string) error {
	if len(args) == 0 {
		if a.Config.DefaultProject == "" {
			fmt.Println("当前没有默认项目")
		} else {
			fmt.Println(a.Config.DefaultProject)
		}
		return nil
	}
	id := args[0]
	if a.Store.Get(id) == nil {
		return fmt.Errorf("项目不存在: %s", id)
	}
	if err := a.Store.SetDefault(id); err != nil {
		return err
	}
	fmt.Printf("default project = %s\n", id)
	return nil
}

func (a *App) removeProject(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: ak project remove <id>")
	}
	id := args[0]
	if err := a.Store.Delete(id); err != nil {
		return err
	}
	cfg := LoadConfig(a.Paths)
	if cfg.DefaultProject == id {
		cfg.DefaultProject = ""
		_ = SaveConfig(a.Paths, cfg)
	}
	fmt.Printf("removed project %s\n", id)
	return nil
}

func (a *App) detectProject(args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}
	proj, err := DetectProject(expandHome(path))
	if err != nil {
		return err
	}
	if a.Opts.JSON {
		return printJSON(proj)
	}
	fmt.Printf("root = %q\n", proj.Root)
	fmt.Printf("package = %q\n", proj.Package)
	fmt.Printf("activity = %q\n", proj.Activity)
	fmt.Printf("module = %q\n", proj.Module)
	fmt.Printf("variant = %q\n", proj.Variant)
	fmt.Printf("apk_glob = %q\n", proj.APKGlob)
	return nil
}

func (a *App) cmdAlias(args []string) error {
	if len(args) == 0 || args[0] == "list" || args[0] == "ls" {
		names := make([]string, 0, len(a.Config.Aliases.Items))
		for name := range a.Config.Aliases.Items {
			names = append(names, name)
		}
		sort.Strings(names)
		if a.Opts.JSON {
			return printJSON(a.Config.Aliases)
		}
		fmt.Printf("preset = %s   force = %t\n\n", a.Config.Aliases.Preset, a.Config.Aliases.Force)
		for _, name := range names {
			fmt.Printf("%-8s → ak %s\n", name, a.Config.Aliases.Items[name])
		}
		return nil
	}
	switch args[0] {
	case "set", "add":
		if len(args) < 3 {
			return fmt.Errorf("用法: ak alias set <名字> <ak 子命令...>")
		}
		name := args[1]
		value := strings.Join(args[2:], " ")
		a.Config.Aliases.Items[name] = value
		if a.Config.Aliases.Preset == "none" {
			a.Config.Aliases.Preset = "custom"
		}
		if err := SaveConfig(a.Paths, a.Config); err != nil {
			return err
		}
		fmt.Printf("%s → ak %s\n", name, value)
		fmt.Println("新终端或按一次回车后生效。")
		return nil
	case "rm", "remove", "delete":
		if len(args) < 2 {
			return fmt.Errorf("用法: ak alias rm <名字>")
		}
		delete(a.Config.Aliases.Items, args[1])
		if err := SaveConfig(a.Paths, a.Config); err != nil {
			return err
		}
		fmt.Printf("removed alias %s\n", args[1])
		return nil
	case "preset":
		if len(args) < 2 {
			return fmt.Errorf("用法: ak alias preset <classic|none>")
		}
		switch args[1] {
		case "classic":
			a.Config.Aliases.Preset = "classic"
			a.Config.Aliases.Items = classicAliases()
		case "none":
			a.Config.Aliases.Preset = "none"
			a.Config.Aliases.Items = map[string]string{}
		default:
			return fmt.Errorf("未知 preset: %s", args[1])
		}
		if err := SaveConfig(a.Paths, a.Config); err != nil {
			return err
		}
		fmt.Printf("alias preset = %s\n", args[1])
		return nil
	case "reset":
		a.Config.Aliases.Preset = "classic"
		a.Config.Aliases.Items = classicAliases()
		if err := SaveConfig(a.Paths, a.Config); err != nil {
			return err
		}
		fmt.Println("aliases reset to classic")
		return nil
	default:
		return fmt.Errorf("用法: ak alias list|set|rm|preset|reset")
	}
}

func (a *App) cmdPaths(args []string) error {
	if a.Opts.JSON {
		return printJSON(a.Paths)
	}
	rows := [][2]string{
		{"binary", a.Paths.Binary},
		{"shim (ak)", a.Paths.Shim},
		{"root", a.Paths.Root},
		{"config", a.Paths.ConfigDir},
		{"config file", a.Paths.ConfigFile()},
		{"projects", a.Paths.ProjectsDir},
		{"data", a.Paths.DataDir},
		{"manifest", a.Paths.Manifest},
		{"backups", a.Paths.BackupDir},
		{"state", a.Paths.StateDir},
		{"web token", a.Paths.WebToken},
		{"reload stamp", a.Paths.Stamp},
		{"logs", a.Paths.LogsDir},
		{"cache", a.Paths.CacheDir},
		{"artifacts", a.Paths.Artifacts},
	}
	fmt.Printf("%-22s %s\n", "ITEM", "PATH")
	for _, row := range rows {
		fmt.Printf("%-22s %s\n", row[0], row[1])
	}
	fmt.Println("\n生成物:")
	fmt.Println("  ~/.android-kit/artifacts/     UI dump / trace / heap / 日志 / 临时文件")
	fmt.Println("  <project>/android-kit/        截图 / Compose 预览 / 可读报告")
	fmt.Println("\n环境变量覆盖: ANDROID_KIT_HOME, ANDROID_KIT_CONFIG_DIR, ANDROID_KIT_DATA_DIR,")
	fmt.Println("              ANDROID_KIT_STATE_DIR, ANDROID_KIT_CACHE_DIR, ANDROID_KIT_BIN_DIR")
	return nil
}
