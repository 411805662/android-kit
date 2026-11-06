package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Project struct {
	ID        string            `toml:"id" json:"id"`
	Name      string            `toml:"name" json:"name"`
	Root      string            `toml:"root" json:"root"`
	Package   string            `toml:"package" json:"package"`
	Activity  string            `toml:"activity" json:"activity"`
	Module    string            `toml:"module" json:"module"`
	Variant   string            `toml:"variant" json:"variant"`
	APKGlob   string            `toml:"apk_glob" json:"apk_glob"`
	Device    string            `toml:"device" json:"device"`
	Env       map[string]string `toml:"env" json:"env"`
	CreatedAt string            `toml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt string            `toml:"updated_at,omitempty" json:"updated_at,omitempty"`
}

type ProjectStore struct {
	Paths    Paths
	Projects map[string]*Project
}

func LoadProjects(p Paths) *ProjectStore {
	store := &ProjectStore{Paths: p, Projects: map[string]*Project{}}
	files, _ := filepath.Glob(filepath.Join(p.ProjectsDir, "*.toml"))
	for _, file := range files {
		var proj Project
		if _, err := toml.DecodeFile(file, &proj); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot parse %s: %v\n", file, err)
			continue
		}
		if proj.ID == "" {
			proj.ID = strings.TrimSuffix(filepath.Base(file), ".toml")
		}
		if proj.Env == nil {
			proj.Env = map[string]string{}
		}
		store.Projects[proj.ID] = &proj
	}
	return store
}

func (s *ProjectStore) List() []*Project {
	out := make([]*Project, 0, len(s.Projects))
	for _, p := range s.Projects {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out
}

func (s *ProjectStore) Get(id string) *Project {
	if id == "" {
		return nil
	}
	if p, ok := s.Projects[id]; ok {
		return p
	}
	for key, p := range s.Projects {
		if strings.EqualFold(key, id) {
			return p
		}
	}
	return nil
}

func (s *ProjectStore) Save(proj *Project) error {
	if proj.ID == "" {
		proj.ID = slug(proj.Name)
	}
	proj.ID = slug(proj.ID)
	if proj.Variant == "" {
		proj.Variant = "debug"
	}
	if proj.Module == "" {
		proj.Module = ":app"
	}
	if proj.Env == nil {
		proj.Env = map[string]string{}
	}
	now := time.Now().Format(time.RFC3339)
	if proj.CreatedAt == "" {
		proj.CreatedAt = now
	}
	proj.UpdatedAt = now
	if strings.HasPrefix(proj.Activity, proj.Package+".") && proj.Package != "" {
		proj.Activity = "." + strings.TrimPrefix(proj.Activity, proj.Package+".")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# android-kit project profile\n")
	fmt.Fprintf(&b, "id = %s\n", strconv.Quote(proj.ID))
	fmt.Fprintf(&b, "name = %s\n", strconv.Quote(proj.Name))
	fmt.Fprintf(&b, "root = %s\n", strconv.Quote(proj.Root))
	fmt.Fprintf(&b, "package = %s\n", strconv.Quote(proj.Package))
	fmt.Fprintf(&b, "activity = %s\n", strconv.Quote(proj.Activity))
	fmt.Fprintf(&b, "module = %s\n", strconv.Quote(proj.Module))
	fmt.Fprintf(&b, "variant = %s\n", strconv.Quote(proj.Variant))
	fmt.Fprintf(&b, "apk_glob = %s\n", strconv.Quote(proj.APKGlob))
	fmt.Fprintf(&b, "device = %s\n", strconv.Quote(proj.Device))
	if proj.CreatedAt != "" {
		fmt.Fprintf(&b, "created_at = %s\n", strconv.Quote(proj.CreatedAt))
	}
	if proj.UpdatedAt != "" {
		fmt.Fprintf(&b, "updated_at = %s\n", strconv.Quote(proj.UpdatedAt))
	}
	if len(proj.Env) > 0 {
		b.WriteString("\n[env]\n")
		keys := make([]string, 0, len(proj.Env))
		for k := range proj.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), strconv.Quote(proj.Env[k]))
		}
	}
	path := filepath.Join(s.Paths.ProjectsDir, proj.ID+".toml")
	if err := atomicWrite(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	s.Projects[proj.ID] = proj
	TouchStamp(s.Paths)
	return nil
}

func (s *ProjectStore) Delete(id string) error {
	proj := s.Get(id)
	if proj == nil {
		return fmt.Errorf("project %q not found", id)
	}
	if err := os.Remove(filepath.Join(s.Paths.ProjectsDir, proj.ID+".toml")); err != nil {
		return err
	}
	delete(s.Projects, proj.ID)
	TouchStamp(s.Paths)
	return nil
}

func (s *ProjectStore) Resolve(flag string) (*Project, error) {
	if flag != "" {
		if p := s.Get(flag); p != nil {
			return p, nil
		}
		return nil, fmt.Errorf("project %q is not registered (run: ak project list)", flag)
	}
	if id := os.Getenv("ANDROID_KIT_PROJECT"); id != "" {
		if p := s.Get(id); p != nil {
			return p, nil
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if p := s.findMarkerUpward(cwd); p != nil {
			return s.ensurePackage(p), nil
		}
		if p := s.matchRootUpward(cwd); p != nil {
			return s.ensurePackage(p), nil
		}
		if p := s.autoRegister(cwd); p != nil {
			return p, nil
		}
	}
	cfg := LoadConfig(s.Paths)
	if cfg.DefaultProject != "" {
		if p := s.Get(cfg.DefaultProject); p != nil {
			return p, nil
		}
	}
	return nil, errNoProject
}

// ensurePackage 补齐项目缺失的包名。正常流程第一次登记时就已经读好了，
// 只有早期导入或 --no-detect 建出来的条目会走到这里，补一次之后就一直沿用。
func (s *ProjectStore) ensurePackage(p *Project) *Project {
	if p == nil || p.Package != "" {
		return p
	}
	detected, err := DetectProject(p.Root)
	if err != nil || detected == nil || detected.Package == "" {
		return p
	}
	p.Package = detected.Package
	if p.Activity == "" {
		p.Activity = detected.Activity
	}
	if p.Module == "" || p.Module == ":" {
		p.Module = detected.Module
	}
	if p.APKGlob == "" {
		p.APKGlob = detected.APKGlob
	}
	if err := s.Save(p); err != nil {
		return p
	}
	fmt.Fprintf(os.Stderr, "ak: 已补全项目 %s 的包名 → %s\n", p.ID, p.Package)
	return p
}

// Refresh 重新探测项目并更新缓存下来的包名等字段。
func (s *ProjectStore) Refresh(p *Project) (*Project, error) {
	if p == nil {
		return nil, errNoProject
	}
	detected, err := DetectProject(p.Root)
	if err != nil {
		return nil, err
	}
	if detected.Package != "" {
		p.Package = detected.Package
	}
	p.Activity = detected.Activity
	if detected.Module != "" {
		p.Module = detected.Module
	}
	if detected.APKGlob != "" {
		p.APKGlob = detected.APKGlob
	}
	if err := s.Save(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *ProjectStore) findMarkerUpward(start string) *Project {
	dir, _ := filepath.Abs(start)
	for {
		marker := filepath.Join(dir, ".android-kit.toml")
		if data, err := os.ReadFile(marker); err == nil {
			var m struct {
				ID string `toml:"id"`
			}
			if _, err := toml.Decode(string(data), &m); err == nil && m.ID != "" {
				if p := s.Get(m.ID); p != nil {
					return p
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func (s *ProjectStore) matchRootUpward(start string) *Project {
	dir, _ := filepath.Abs(start)
	bestLen := -1
	var best *Project
	for _, p := range s.Projects {
		root, err := filepath.Abs(p.Root)
		if err != nil {
			continue
		}
		if dir == root || strings.HasPrefix(dir, root+string(os.PathSeparator)) {
			if len(root) > bestLen {
				bestLen = len(root)
				best = p
			}
		}
	}
	return best
}

// autoRegister 在未登记的 Android 工程目录里自动登记项目：cd 到哪个项目，
// ak 命令就作用于哪个项目，不用先手动 ak project add。
// 设 AK_NO_AUTO_REGISTER=1 可关掉。
func (s *ProjectStore) autoRegister(start string) *Project {
	if os.Getenv("AK_NO_AUTO_REGISTER") != "" {
		return nil
	}
	root, hint := findAndroidRoot(start)
	if root == "" {
		return nil
	}
	proj, err := DetectProject(root)
	if err != nil || proj == nil || proj.Package == "" {
		// 没有包名的多半不是 Android 应用工程，不自动登记
		return nil
	}
	// 工程在 repo/android 这种子目录里时，用仓库目录名当项目名
	if hint != "" {
		proj.Name = hint
		proj.ID = slug(hint)
	}
	if existing := s.Get(proj.ID); existing != nil {
		// 同一个工程（比如从仓库根目录进来，root 在子目录里）直接复用，别重复登记
		if filepath.Clean(existing.Root) == filepath.Clean(root) {
			return s.ensurePackage(existing)
		}
		// 目录同名但不是同一个工程，用路径后缀区分
		proj.ID = proj.ID + "-" + shortHash(root)
	}
	if err := s.Save(proj); err != nil {
		return nil
	}
	fmt.Fprintf(os.Stderr, "ak: 已自动登记项目 %s → %s\n", proj.ID, proj.Root)
	return proj
}

func (s *ProjectStore) SetDefault(id string) error {
	cfg := LoadConfig(s.Paths)
	cfg.DefaultProject = id
	return SaveConfig(s.Paths, cfg)
}

func (s *ProjectStore) APK(proj *Project) (string, error) {
	root := proj.Root
	if root == "" || !isDir(root) {
		return "", fmt.Errorf("project root does not exist: %s", root)
	}
	var candidates []string
	seen := map[string]bool{}
	addGlob := func(pattern string) {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			if st, err := os.Stat(m); err == nil && !st.IsDir() {
				candidates = append(candidates, m)
			}
		}
	}
	if proj.APKGlob != "" {
		addGlob(proj.APKGlob)
	}
	variant := proj.Variant
	if variant == "" {
		variant = "debug"
	}
	moduleDir := ""
	if proj.Module != "" && proj.Module != ":" {
		moduleDir = strings.TrimPrefix(strings.ReplaceAll(proj.Module, ":", string(os.PathSeparator)), string(os.PathSeparator))
	}
	if moduleDir != "" {
		addGlob(filepath.Join(moduleDir, "build", "outputs", "apk", variant, "*.apk"))
		addGlob(filepath.Join(moduleDir, "build", "intermediates", "apk", variant, "*.apk"))
		addGlob(filepath.Join(moduleDir, "build", "intermediates", "apk_ide_redirect_file", variant, "*", "*.apk"))
	}
	addGlob(filepath.Join("app", "build", "outputs", "apk", variant, "*.apk"))
	addGlob(filepath.Join("app", "build", "intermediates", "apk", variant, "*.apk"))
	addGlob(filepath.Join("*", "build", "outputs", "apk", variant, "*.apk"))
	addGlob(filepath.Join("*", "*", "build", "outputs", "apk", variant, "*.apk"))
	if len(candidates) == 0 {
		return "", fmt.Errorf("no %s APK found under %s (build first or set apk_glob)", variant, root)
	}
	sort.Slice(candidates, func(i, j int) bool {
		si, _ := os.Stat(candidates[i])
		sj, _ := os.Stat(candidates[j])
		if si == nil || sj == nil {
			return candidates[i] < candidates[j]
		}
		return si.ModTime().After(sj.ModTime())
	})
	return candidates[0], nil
}

var (
	reApplicationID  = regexp.MustCompile(`(?m)^\s*applicationId\s*=?\s*["']([^"']+)["']`)
	reNamespace      = regexp.MustCompile(`(?m)^\s*namespace\s*=?\s*["']([^"']+)["']`)
	reInclude        = regexp.MustCompile(`(?m)^\s*include\s*\(?\s*["'](:?[^"']+)["']`)
	reActivity       = regexp.MustCompile(`(?s)<activity\b([^>]*)>(.*?)</activity>`)
	reActivityAlias  = regexp.MustCompile(`(?s)<activity-alias\b([^>]*)>(.*?)</activity-alias>`)
	reAndroidName    = regexp.MustCompile(`android:name\s*=\s*"([^"]+)"`)
	reTargetActivity = regexp.MustCompile(`android:targetActivity\s*=\s*"([^"]+)"`)
	reManifestPkg    = regexp.MustCompile(`<manifest\b[^>]*\bpackage\s*=\s*"([^"]+)"`)
)

func DetectProject(input string) (*Project, error) {
	if input == "" {
		input = "."
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	root, nameHint := findAndroidRoot(abs)
	if root == "" {
		return nil, fmt.Errorf("no settings.gradle(.kts) found above %s", abs)
	}
	proj := &Project{
		Name:    filepath.Base(root),
		Root:    root,
		Module:  ":app",
		Variant: "debug",
		Env:     map[string]string{},
	}
	if nameHint != "" {
		proj.Name = nameHint
	}
	proj.ID = slug(proj.Name)
	if isDir(filepath.Join(root, "app")) {
		proj.Module = ":app"
	}
	modules := detectModules(root)
	if len(modules) > 0 && !contains(modules, proj.Module) {
		proj.Module = modules[0]
	}

	if pkg, mod := detectPackage(root); pkg != "" {
		proj.Package = pkg
		if mod != "" {
			proj.Module = mod
		}
	}
	if proj.Package == "" {
		proj.Package = detectManifestPackage(root)
	}
	proj.Activity = detectLauncherActivity(root)
	proj.APKGlob = apkGlobFor(proj.Module, proj.Variant)
	return proj, nil
}

func findGradleRoot(start string) string {
	dir := start
	if !isDir(dir) {
		dir = filepath.Dir(dir)
	}
	for {
		for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
			if exists(filepath.Join(dir, name)) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// findAndroidRoot 先用常规的「向上找 settings.gradle」；找不到时再看当前目录，
// 如果只有一个子目录是 Gradle 工程（android/、app/ 这类常见仓库布局），就用它，
// 并把上一级目录名当作项目名。多个子目录都像工程时不做猜测。
func findAndroidRoot(start string) (root, nameHint string) {
	if r := findGradleRoot(start); r != "" {
		return r, ""
	}
	dir := start
	if !isDir(dir) {
		dir = filepath.Dir(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	var hits []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
			if exists(filepath.Join(sub, name)) {
				hits = append(hits, sub)
				break
			}
		}
	}
	if len(hits) != 1 {
		return "", ""
	}
	return hits[0], filepath.Base(dir)
}

func detectModules(root string) []string {
	var out []string
	for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		for _, m := range reInclude.FindAllStringSubmatch(string(data), -1) {
			mod := ":" + strings.TrimPrefix(strings.ReplaceAll(m[1], ":", "/"), "/")
			out = append(out, mod)
		}
	}
	return out
}

func detectPackage(root string) (string, string) {
	var bestPkg, bestMod string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(os.PathSeparator)) + 1
		}
		if d.IsDir() {
			if d.Name() == "build" || strings.HasPrefix(d.Name(), ".") || depth > 4 {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "build.gradle" && name != "build.gradle.kts" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		if m := reApplicationID.FindStringSubmatch(text); len(m) > 1 {
			dir := filepath.Dir(path)
			relDir, _ := filepath.Rel(root, dir)
			mod := ":"
			if relDir != "." {
				mod = ":" + strings.ReplaceAll(relDir, string(os.PathSeparator), ":")
			}
			if bestPkg == "" || mod == ":app" {
				bestPkg, bestMod = m[1], mod
			}
		}
		return nil
	})
	return bestPkg, bestMod
}

func detectManifestPackage(root string) string {
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			depth := 0
			if rel != "." {
				depth = strings.Count(rel, string(os.PathSeparator)) + 1
			}
			if depth > 5 || d.Name() == "build" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "AndroidManifest.xml" || found != "" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if m := reManifestPkg.FindStringSubmatch(string(data)); len(m) > 1 {
			found = m[1]
		}
		return nil
	})
	return found
}

func detectLauncherActivity(root string) string {
	var manifests []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if d.Name() == "build" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if strings.Count(rel, string(os.PathSeparator)) > 5 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "AndroidManifest.xml" {
			manifests = append(manifests, path)
		}
		return nil
	})
	sort.SliceStable(manifests, func(i, j int) bool {
		ai := strings.Contains(manifests[i], string(os.PathSeparator)+"app"+string(os.PathSeparator))
		aj := strings.Contains(manifests[j], string(os.PathSeparator)+"app"+string(os.PathSeparator))
		return ai && !aj
	})
	type candidate struct {
		name  string
		score int
	}
	var candidates []candidate
	for _, path := range manifests {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(data)
		appManifest := strings.Contains(path, string(os.PathSeparator)+"app"+string(os.PathSeparator))
		for _, m := range reActivity.FindAllStringSubmatch(text, -1) {
			if !strings.Contains(m[2], "android.intent.category.LAUNCHER") {
				continue
			}
			nm := reAndroidName.FindStringSubmatch(m[1])
			if len(nm) < 2 {
				continue
			}
			candidates = append(candidates, candidate{name: nm[1], score: activityScore(nm[1], appManifest)})
		}
		for _, m := range reActivityAlias.FindAllStringSubmatch(text, -1) {
			if !strings.Contains(m[2], "android.intent.category.LAUNCHER") {
				continue
			}
			name := ""
			if tm := reTargetActivity.FindStringSubmatch(m[1]); len(tm) > 1 {
				name = tm[1]
			} else if nm := reAndroidName.FindStringSubmatch(m[1]); len(nm) > 1 {
				name = nm[1]
			}
			if name == "" {
				continue
			}
			candidates = append(candidates, candidate{name: name, score: activityScore(name, appManifest) + 5})
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	name := candidates[0].name
	if strings.HasPrefix(name, ".") {
		return name
	}
	if strings.Contains(name, ".") {
		return name
	}
	return "." + name
}

func activityScore(name string, appManifest bool) int {
	score := 0
	lower := strings.ToLower(name)
	if strings.Contains(lower, "splash") {
		score += 100
	}
	if strings.Contains(lower, "main") {
		score += 50
	}
	if strings.Contains(lower, "launch") {
		score += 40
	}
	if appManifest {
		score += 20
	}
	return score
}

func apkGlobFor(module, variant string) string {
	dir := ""
	if module != "" && module != ":" {
		dir = strings.TrimPrefix(strings.ReplaceAll(module, ":", "/"), "/")
	}
	if dir == "" {
		dir = "app"
	}
	return filepath.ToSlash(filepath.Join(dir, "build", "outputs", "apk", variant, "*.apk"))
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
