package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	reImportRoot    = regexp.MustCompile(`(?m)^\s*export\s+PROJECT_ROOT\s*=\s*["']?([^"'\n]+)["']?`)
	reImportPkg     = regexp.MustCompile(`(?m)^\s*export\s+APP_NAME\s*=\s*["']?([^"'\n]+)["']?`)
	reImportAct     = regexp.MustCompile(`(?m)^\s*export\s+MAIN_ACTIVITY\s*=\s*["']?([^"'\n]+)["']?`)
	reImportAPKPath = regexp.MustCompile(`(?m)^\s*export\s+DEBUG_APK_PATH\s*=\s*["']?([^"'\n]+)["']?`)
	reAliasInstall  = regexp.MustCompile(`alias\s+in\d*\s*=\s*["']adb install[^"'\n]*?(/[^"'\s]+\.apk)`)
	reAliasBuild    = regexp.MustCompile(`alias\s+b\d*\s*=\s*["']cd\s+([~/][^"'\n&]+)`)
	reAliasStop     = regexp.MustCompile(`alias\s+k\d*\s*=\s*["']adb shell am force-stop\s+([^\s"']+)`)
	reAliasOpen     = regexp.MustCompile(`alias\s+o\d*\s*=\s*["']adb shell am start\s+([^\s/"']+)/([^\s"']+)`)
)

func (a *App) cmdImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: ak import <旧 android-toolkit 目录 | .bash_profile | .zshrc>")
	}
	target := expandHome(args[0])
	var files []string
	if isDir(target) {
		for _, name := range []string{"config.sh", "aliases.sh", ".bash_profile", ".zshrc"} {
			path := filepath.Join(target, name)
			if exists(path) {
				files = append(files, path)
			}
		}
	} else if exists(target) {
		files = append(files, target)
	} else {
		return fmt.Errorf("文件或目录不存在: %s", target)
	}
	if len(files) == 0 {
		return fmt.Errorf("没有找到 config.sh / aliases.sh / .bash_profile / .zshrc")
	}

	var all strings.Builder
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		all.Write(data)
		all.WriteString("\n")
	}
	text := all.String()

	envRoot := firstMatch(reImportRoot, text)
	envPkg := firstMatch(reImportPkg, text)
	envAct := firstMatch(reImportAct, text)
	envAPK := firstMatch(reImportAPKPath, text)

	roots := map[string]*Project{}
	addRoot := func(root string) *Project {
		root = strings.TrimSpace(root)
		root = strings.TrimSuffix(root, "/")
		if root == "" || strings.ContainsAny(root, "$&") || !strings.HasPrefix(root, "/") {
			return nil
		}
		if p, ok := roots[root]; ok {
			return p
		}
		p := &Project{Name: filepath.Base(root), Root: root, Module: ":app", Variant: "debug", Env: map[string]string{}}
		base := slug(p.Name)
		p.ID = base
		for i := 2; ; i++ {
			conflict := false
			for _, other := range roots {
				if other.ID == p.ID && other.Root != root {
					conflict = true
					break
				}
			}
			if existing := a.Store.Get(p.ID); existing != nil && existing.Root != root {
				conflict = true
			}
			if !conflict {
				break
			}
			p.ID = fmt.Sprintf("%s-%d", base, i)
		}
		roots[root] = p
		return p
	}
	if envRoot != "" {
		if p := addRoot(envRoot); p != nil {
			p.Package = envPkg
			p.Activity = envAct
			if envAPK != "" {
				p.APKGlob = envAPK
			}
		}
	}
	for _, m := range reAliasInstall.FindAllStringSubmatch(text, -1) {
		apk := m[1]
		root := rootFromAPK(apk)
		if p := addRoot(root); p != nil && p.APKGlob == "" {
			p.APKGlob = strings.TrimPrefix(apk, root+"/")
		}
	}
	for _, m := range reAliasBuild.FindAllStringSubmatch(text, -1) {
		addRoot(m[1])
	}
	for _, m := range reAliasStop.FindAllStringSubmatch(text, -1) {
		for _, p := range roots {
			if p.Package == "" {
				p.Package = m[1]
			}
		}
	}
	for _, m := range reAliasOpen.FindAllStringSubmatch(text, -1) {
		for _, p := range roots {
			if p.Package == "" {
				p.Package = m[1]
			}
			if p.Activity == "" {
				p.Activity = m[2]
			}
		}
	}
	if len(roots) == 0 {
		return fmt.Errorf("没有从 %s 中识别出项目路径", strings.Join(files, ", "))
	}

	keys := make([]string, 0, len(roots))
	for root := range roots {
		keys = append(keys, root)
	}
	sort.Strings(keys)
	for _, root := range keys {
		p := roots[root]
		if isDir(root) {
			if detected, err := DetectProject(root); err == nil {
				if p.Package == "" {
					p.Package = detected.Package
				}
				if p.Activity == "" {
					p.Activity = detected.Activity
				}
				if p.Module == "" || p.Module == ":app" {
					p.Module = detected.Module
				}
				if p.APKGlob == "" {
					p.APKGlob = detected.APKGlob
				}
			}
		}
		if p.APKGlob == "" {
			p.APKGlob = apkGlobFor(p.Module, p.Variant)
		}
		if err := a.Store.Save(p); err != nil {
			return err
		}
		fmt.Printf("imported %-16s %s\n", p.ID, p.Root)
		fmt.Printf("  package:  %s\n", orDash(p.Package))
		fmt.Printf("  activity: %s\n", orDash(p.Activity))
	}
	if a.Config.DefaultProject == "" {
		_ = a.Store.SetDefault(roots[keys[0]].ID)
		fmt.Printf("default project = %s\n", roots[keys[0]].ID)
	}
	return nil
}

func firstMatch(re *regexp.Regexp, text string) string {
	m := re.FindStringSubmatch(text)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func rootFromAPK(apk string) string {
	for _, marker := range []string{"/app/build/", "/build/"} {
		if idx := strings.Index(apk, marker); idx > 0 {
			return apk[:idx]
		}
	}
	return filepath.Dir(filepath.Dir(apk))
}
