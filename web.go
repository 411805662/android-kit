package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/index.html
//go:embed web/viewer.html
//go:embed web/json.html
var webFS embed.FS

var webRunMu sync.Mutex

// webPageTheme is replaced in the served HTML so the saved theme applies on the
// very first paint, without a flash of the other theme.
const webPageTheme = "__AK_THEME__"

func (a *App) readUITheme() string {
	data, err := os.ReadFile(a.Paths.UITheme)
	if err != nil {
		return "auto"
	}
	t := strings.TrimSpace(string(data))
	if t != "light" && t != "dark" {
		return "auto"
	}
	return t
}

func (a *App) writeUITheme(t string) error {
	if t != "light" && t != "dark" && t != "auto" {
		return fmt.Errorf("未知主题: %s", t)
	}
	return atomicWrite(a.Paths.UITheme, []byte(t+"\n"), 0o644)
}

func (a *App) webPage(name string) ([]byte, error) {
	data, err := webFS.ReadFile("web/" + name)
	if err != nil {
		return nil, err
	}
	return []byte(strings.ReplaceAll(string(data), webPageTheme, a.readUITheme())), nil
}

type CommandInfo struct {
	ID        string   `json:"id"`
	Target    string   `json:"target"`
	Group     string   `json:"group"`
	Title     string   `json:"title"`
	Desc      string   `json:"desc"`
	Command   string   `json:"command"`
	Usage     string   `json:"usage"`
	Engine    string   `json:"engine"`
	Available bool     `json:"available"`
	Runnable  bool     `json:"runnable"`
	Run       string   `json:"run"`
	Aliases   []string `json:"aliases"`
	Examples  []string `json:"examples"`
	// Hidden 的命令仍然可用，只是暂时不在网页配置台里展示。
	Hidden bool `json:"hidden,omitempty"`
}

type CustomAlias struct {
	Alias   string `json:"alias"`
	Command string `json:"command"`
}

type WebState struct {
	Version     string            `json:"version"`
	Config      Config            `json:"config"`
	Projects    []*Project        `json:"projects"`
	Current     *Project          `json:"current"`
	Paths       Paths             `json:"paths"`
	Runtime     Runtime           `json:"runtime"`
	Devices     []Device          `json:"devices"`
	Commands    []CommandInfo     `json:"commands"`
	Custom      []CustomAlias     `json:"custom"`
	ProjectDirs map[string]string `json:"project_dirs"`
	Artifacts   []ArtifactItem    `json:"artifacts"`
	Display     DisplayInfo       `json:"display"`
}

func (a *App) webState() WebState {
	proj, _ := a.project(false)
	rt := detectRuntime(a.Paths, a.Config)
	engine := "legacy"
	if rt.HasAndroidCLI() && versionAtLeast(rt.AndroidCLIversion, a.Config.AndroidCLI.MinVersion) {
		engine = "android"
	}
	dirs := map[string]string{}
	if proj != nil {
		hidden, visible, err := a.Paths.EnsureProjectDirs(proj)
		if err == nil {
			dirs["hidden"] = hidden
			dirs["visible"] = visible
		}
	}
	return WebState{
		Version:     appVersion,
		Config:      a.Config,
		Projects:    a.Store.List(),
		Current:     proj,
		Paths:       a.Paths,
		Runtime:     rt,
		Devices:     a.Paths.Devices(),
		Commands:    visibleCommands(a.commandCatalog(proj, engine)),
		Custom:      a.customAliases(),
		ProjectDirs: dirs,
		Artifacts:   a.projectArtifacts(""),
		Display:     a.displayInfo(""),
	}
}

// visibleCommands 去掉暂时不展示的命令，CLI 侧不受影响。
func visibleCommands(items []CommandInfo) []CommandInfo {
	out := make([]CommandInfo, 0, len(items))
	for _, item := range items {
		if item.Hidden {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (a *App) customAliases() []CustomAlias {
	out := []CustomAlias{}
	for name, value := range a.Config.Aliases.Items {
		if strings.HasPrefix(strings.TrimSpace(value), "shell:") {
			out = append(out, CustomAlias{Alias: name, Command: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "shell:"))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

const (
	groupProject      = "项目"
	groupProjectAdmin = "项目管理"
	groupDeploy       = "构建 · 安装 · 卸载 · 部署"
	groupApp          = "启动 · 杀进程 · 重启"
	groupDebug        = "清数据 · 截图 · UI 树 · 文本 · 存储路径"
	groupOther        = "其他"
)

func (a *App) commandCatalog(proj *Project, engine string) []CommandInfo {
	aliasMap := map[string][]string{}
	for name, value := range a.Config.Aliases.Items {
		aliasMap[strings.TrimSpace(value)] = append(aliasMap[strings.TrimSpace(value)], name)
	}
	root := "<project>"
	pkg := "<package>"
	module := ":app"
	variant := "debug"
	apk := "<apk>"
	dev := ""
	if proj != nil {
		root = proj.Root
		pkg = orDash(proj.Package)
		module = proj.Module
		variant = proj.Variant
		dev = a.device(proj)
	}
	if module == "" {
		module = ":app"
	}
	if variant == "" {
		variant = "debug"
	}
	devFlag := ""
	if dev != "" {
		devFlag = " --device " + dev
	}
	mod := strings.TrimPrefix(module, ":")
	if mod == "" {
		mod = "app"
	}
	moduleName := path.Base(strings.ReplaceAll(module, ":", "/"))
	if moduleName == "." || moduleName == "/" || moduleName == "" {
		moduleName = "app"
	}
	if proj != nil {
		if p, err := a.Store.APK(proj); err == nil {
			apk = p
		} else {
			dir := strings.TrimSuffix(apkGlobFor(module, variant), "*.apk")
			apk = filepath.Join(proj.Root, filepath.FromSlash(dir+moduleName+"-"+variant+".apk"))
		}
	}
	task := mod + ":assemble" + strings.ToUpper(variant[:1]) + variant[1:]
	gradleBuild := "./gradlew " + gradleTask(module, variant)
	gradleClean := "./gradlew clean " + gradleTask(module, variant)
	buildCmd := gradleBuild
	cleanCmd := gradleClean
	installCmd := "adb install -r -t -d " + apk
	startCmd := "adb shell am start -n $(adb shell cmd package resolve-activity --brief -c android.intent.category.LAUNCHER " + pkg + " | tail -1)"
	runCmd := gradleBuild + " && " + installCmd + " && " + startCmd
	layoutCmd := "adb shell uiautomator dump /sdcard/android-kit-ui.xml && adb exec-out cat /sdcard/android-kit-ui.xml"
	shotCmd := "adb exec-out screencap -p > " + root + "/android-kit/screenshots/<time>.png"
	if engine == "android" {
		buildCmd = "android build " + task
		cleanCmd = "android build " + mod + ":clean && android build " + task
		installCmd = "android install " + apk + devFlag
		runCmd = "android run " + apk + devFlag
		layoutCmd = "android layout -p" + devFlag
		shotCmd = "android screen capture --output " + root + "/android-kit/screenshots/<time>.png" + devFlag
	}
	items := []CommandInfo{
		{ID: "web", Target: "web", Group: groupProject, Title: "网页配置台", Desc: "本地 127.0.0.1，token 保护", Command: "ak web", Engine: "-", Available: true, Runnable: false},
		{ID: "cd", Target: "cd", Group: groupProject, Title: "进入项目目录", Desc: "shell 函数，直接 cd 到项目根目录", Command: "cd " + root, Engine: "-", Available: true, Runnable: false},
		{ID: "devices", Target: "devices", Group: groupProject, Title: "设备列表", Desc: "adb devices -l", Command: "adb devices -l", Engine: "adb", Available: true, Runnable: true, Run: "devices"},
		{ID: "status", Target: "status", Group: groupProject, Title: "状态", Desc: "项目、设备、引擎、APK", Command: "ak status", Engine: "-", Available: true, Runnable: true, Run: "status"},
		{ID: "doctor", Target: "doctor", Group: groupProject, Title: "环境诊断", Desc: "逐项检查 adb / CLI / Java / 项目", Command: "ak doctor", Engine: "-", Available: true, Runnable: true, Run: "doctor"},

		// 项目管理工作流暂时隐藏（CLI 仍然可用），cd 到项目目录即自动生效。
		{ID: "project-add", Target: "project add", Group: groupProjectAdmin, Title: "登记项目", Desc: "自动探测包名、模块、APK", Command: "ak project add <path>", Engine: "-", Available: true, Runnable: false, Hidden: true},
		{ID: "project-list", Target: "project list", Group: groupProjectAdmin, Title: "项目列表", Desc: "列出已登记项目", Command: "ak project list", Engine: "-", Available: true, Runnable: true, Run: "project list", Hidden: true},
		{ID: "project-show", Target: "project show", Group: groupProjectAdmin, Title: "查看项目", Desc: "显示项目配置", Command: "ak project show <id>", Engine: "-", Available: true, Runnable: false, Hidden: true},
		{ID: "project-use", Target: "project use", Group: groupProjectAdmin, Title: "切换项目", Desc: "设为默认项目", Command: "ak project use <id>", Engine: "-", Available: true, Runnable: false, Hidden: true},
		{ID: "project-remove", Target: "project remove", Group: groupProjectAdmin, Title: "删除项目", Desc: "删除项目配置，不删文件", Command: "ak project remove <id>", Engine: "-", Available: true, Runnable: false, Hidden: true},
		{ID: "use", Target: "use", Group: groupProjectAdmin, Title: "切换项目（简写）", Desc: "同 ak project use", Command: "ak use <id>", Engine: "-", Available: true, Runnable: false, Hidden: true},

		{ID: "build", Target: "build", Group: groupDeploy, Title: "构建 Debug", Desc: "编译当前模块的 debug 变体", Command: buildCmd, Engine: engine, Available: true, Runnable: true, Run: "build"},
		{ID: "build-clean", Target: "build --clean", Group: groupDeploy, Title: "清理后构建", Desc: "先 clean 再编译，解决缓存问题", Command: cleanCmd, Engine: engine, Available: true, Runnable: true, Run: "build --clean"},
		{ID: "install", Target: "install", Group: groupDeploy, Title: "安装 APK", Desc: "自动找最新 APK；官方 CLI 支持 delta install", Command: installCmd, Engine: engine, Available: true, Runnable: true, Run: "install"},
		{ID: "uninstall", Target: "uninstall", Group: groupDeploy, Title: "卸载 App", Desc: "卸载手机上的应用；卸载本工具用 ak unset", Command: "adb uninstall " + pkg, Engine: "adb", Available: true, Runnable: true, Run: "uninstall"},
		{ID: "run", Target: "run", Group: groupDeploy, Title: "构建 + 安装 + 启动", Desc: "一条命令完成日常部署", Command: runCmd, Engine: engine, Available: true, Runnable: true, Run: "run"},

		{ID: "open", Target: "open", Group: groupApp, Title: "启动应用", Desc: "启动 launcher 默认 Activity，不用配 Activity", Command: startCmd, Engine: "adb", Available: true, Runnable: true, Run: "open"},
		{ID: "stop", Target: "stop", Group: groupApp, Title: "杀进程", Desc: "force-stop 当前包名", Command: "adb shell am force-stop " + pkg, Engine: "adb", Available: true, Runnable: true, Run: "stop"},
		{ID: "restart", Target: "restart", Group: groupApp, Title: "重启应用", Desc: "杀进程后立即启动", Command: "adb shell am force-stop " + pkg + " && " + startCmd, Engine: "adb", Available: true, Runnable: true, Run: "restart"},

		{ID: "clear", Target: "clear", Group: groupDebug, Title: "清除数据", Desc: "pm clear，登录态会丢失", Command: "adb shell pm clear " + pkg, Engine: "adb", Available: true, Runnable: true, Run: "clear"},
		{ID: "screenshot", Target: "screenshot", Group: groupDebug, Title: "截图", Desc: "保存到 android-kit/screenshots", Command: shotCmd, Engine: engine, Available: true, Runnable: true, Run: "screenshot"},
		{ID: "layout", Target: "layout -p", Group: groupDebug, Title: "UI 树", Desc: "截图 + UI 树，输出 H5 对照链接", Command: layoutCmd, Engine: engine, Available: true, Runnable: true, Run: "layout -p"},
		{ID: "text", Target: "text", Group: groupDebug, Title: "输入文本", Desc: "空格自动转 %s", Command: "adb shell input text <text>", Engine: "adb", Available: true, Runnable: false},
		{ID: "paths", Target: "paths", Group: groupDebug, Title: "存储路径", Desc: "查看所有目录和文件位置", Command: "ak paths", Engine: "-", Available: true, Runnable: true, Run: "paths"},

		{ID: "logcat", Target: "logcat", Group: groupOther, Title: "应用日志", Desc: "默认只看当前应用；后接字符串模糊匹配 tag 或 message", Command: "adb logcat -v time --pid=$(adb shell pidof " + pkg + ") | grep -i <pattern>", Engine: "adb", Available: true, Runnable: false},
		{ID: "tap", Target: "tap", Group: groupOther, Title: "点击", Desc: "坐标或选择器", Command: "adb shell input tap <x> <y>", Engine: "adb", Available: true, Runnable: false},
		{ID: "swipe", Target: "swipe", Group: groupOther, Title: "滑动", Desc: "四个坐标 + 可选时长", Command: "adb shell input swipe <x1> <y1> <x2> <y2> [ms]", Engine: "adb", Available: true, Runnable: false},
		{ID: "key", Target: "key", Group: groupOther, Title: "按键", Desc: "keyevent，如 4=返回 3=Home", Command: "adb shell input keyevent <code>", Engine: "adb", Available: true, Runnable: false},
		{ID: "preview", Target: "preview", Group: groupOther, Title: "Compose 预览", Desc: "无设备渲染 @Preview 为 PNG", Command: "android preview render --output " + root + "/android-kit/previews", Engine: "android", Available: engine == "android", Runnable: false},
		{ID: "analyze", Target: "analyze", Group: groupOther, Title: "APK 分析", Desc: "DEX / R8 / keep 规则 / native 库", Command: "android analyze " + apk, Engine: "android", Available: engine == "android", Runnable: false},
		{ID: "docs", Target: "docs", Group: groupOther, Title: "官方文档", Desc: "搜索 Android Knowledge Base", Command: "android docs search <query>", Engine: "android", Available: engine == "android", Runnable: false},
		{ID: "clean", Target: "clean", Group: groupOther, Title: "清理生成物", Desc: "默认清 tmp/logs", Command: "ak clean", Engine: "-", Available: true, Runnable: true, Run: "clean"},
	}
	for i := range items {
		items[i].Usage = "ak " + items[i].Target
		items[i].Aliases = aliasMap[items[i].Target]
	}
	return items
}

func randomToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (a *App) cmdWeb(args []string) error {
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	port := flags.Int("port", 0, "端口，0 表示随机")
	noOpen := flags.Bool("no-open", false, "不自动打开浏览器")
	printURL := flags.Bool("print-url", false, "只打印 URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	token := randomToken()
	if err := os.MkdirAll(a.Paths.StateDir, 0o755); err != nil {
		return err
	}
	if err := atomicWrite(a.Paths.WebToken, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", actualPort, token)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	if err := atomicWrite(a.Paths.WebURL, []byte(baseURL+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(a.Paths.WebURL)
	fmt.Println(url)
	if *printURL {
		ln.Close()
		return nil
	}
	if !*noOpen && a.Config.UI.OpenBrowser {
		openBrowser(url)
	}

	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get("X-AK-Token")
			if got == "" {
				got = r.URL.Query().Get("token")
			}
			if got == "" {
				if c, err := r.Cookie("ak_token"); err == nil {
					got = c.Value
				}
			}
			if got != token {
				http.Error(w, "invalid token", http.StatusForbidden)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("token") == token {
			http.SetCookie(w, &http.Cookie{Name: "ak_token", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		}
		data, err := a.webPage("index.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.HandleFunc("/api/theme", auth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeHTTPJSON(w, map[string]any{"theme": a.readUITheme()})
		case http.MethodPost, http.MethodPut:
			var body struct {
				Theme string `json:"theme"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if err := a.writeUITheme(body.Theme); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeHTTPJSON(w, map[string]any{"theme": body.Theme})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/artifact", auth(a.serveArtifact))
	mux.HandleFunc("/api/artifact", auth(a.deleteArtifact))
	mux.HandleFunc("/api/artifacts/clear", auth(a.clearArtifacts))
	mux.HandleFunc("/viewer", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") == token {
			http.SetCookie(w, &http.Cookie{Name: "ak_token", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		}
		data, err := a.webPage("viewer.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}))
	mux.HandleFunc("/api/artifacts", auth(func(w http.ResponseWriter, r *http.Request) {
		a.reload()
		writeHTTPJSON(w, map[string]any{
			"artifacts": a.projectArtifacts(r.URL.Query().Get("project")),
			"display":   a.displayInfo(r.URL.Query().Get("project")),
		})
	}))
	mux.HandleFunc("/api/activity", auth(func(w http.ResponseWriter, r *http.Request) {
		a.reload()
		proj, _ := a.webProject(r.URL.Query().Get("project"))
		writeHTTPJSON(w, map[string]string{"activity": a.currentActivity(proj)})
	}))
	mux.HandleFunc("/api/state", auth(func(w http.ResponseWriter, r *http.Request) {
		a.reload()
		writeHTTPJSON(w, a.webState())
	}))
	mux.HandleFunc("/api/doctor", auth(func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/api/detect", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		proj, err := DetectProject(expandHome(body.Path))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeHTTPJSON(w, proj)
	}))
	mux.HandleFunc("/api/project", auth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var proj Project
			if err := json.NewDecoder(r.Body).Decode(&proj); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if proj.Root == "" || !isDir(proj.Root) {
				http.Error(w, "项目目录不存在: "+proj.Root, 400)
				return
			}
			if proj.ID == "" {
				proj.ID = slug(proj.Name)
			}
			if err := a.Store.Save(&proj); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			a.reload()
			writeHTTPJSON(w, map[string]any{"ok": true, "project": proj, "applied": "immediate"})
		case http.MethodDelete:
			id := r.URL.Query().Get("id")
			if err := a.Store.Delete(id); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			a.reload()
			writeHTTPJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", 405)
		}
	}))
	mux.HandleFunc("/api/project/default", auth(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := a.Store.SetDefault(body.ID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		a.reload()
		writeHTTPJSON(w, map[string]any{"ok": true})
	}))
	mux.HandleFunc("/api/config", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		var cfg Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if cfg.Aliases.Items == nil {
			cfg.Aliases.Items = map[string]string{}
		}
		if err := SaveConfig(a.Paths, cfg); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		a.reload()
		writeHTTPJSON(w, map[string]any{"ok": true, "applied": "immediate"})
	}))
	mux.HandleFunc("/api/aliases", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		var body struct {
			Items map[string]string `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.reload()
		cfg := a.Config
		if body.Items == nil {
			body.Items = map[string]string{}
		}
		cfg.Aliases.Items = body.Items
		cfg.Aliases.Preset = "custom"
		if err := SaveConfig(a.Paths, cfg); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		a.reload()
		writeHTTPJSON(w, map[string]any{"ok": true, "count": len(body.Items)})
	}))
	mux.HandleFunc("/api/run", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		if !a.Config.UI.AllowRunFromWeb {
			http.Error(w, "网页执行已关闭：在下方「全局设置」里勾选「允许网页执行命令」并保存", 403)
			return
		}
		var body struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		fields := append(strings.Fields(body.Command), body.Args...)
		if len(fields) == 0 {
			http.Error(w, "缺少 command", 400)
			return
		}
		allowed := map[string]bool{
			"build": true, "install": true, "run": true, "stop": true, "open": true,
			"restart": true, "clear": true, "screenshot": true, "layout": true,
			"uninstall": true, "logcat": true, "devices": true, "status": true,
			"doctor": true, "paths": true, "clean": true,
		}
		if !allowed[fields[0]] {
			if fields[0] == "project" && len(fields) > 1 {
				switch fields[1] {
				case "list", "show":
					goto allowedProjectCommand
				}
			}
			http.Error(w, "command not allowed", 403)
			return
		}
	allowedProjectCommand:
		if !webRunMu.TryLock() {
			http.Error(w, "已有命令正在执行，请等它结束", 409)
			return
		}
		defer webRunMu.Unlock()

		proj, _ := a.project(false)
		args := fields
		if proj != nil {
			args = append([]string{"-p", proj.ID}, args...)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, a.Paths.Binary, args...)
		if proj != nil {
			cmd.Dir = proj.Root
		}
		cmd.Env = append(os.Environ(), "ANDROID_KIT_NO_ALIASES=1", "NO_COLOR=1")
		started := time.Now()
		out, err := cmd.CombinedOutput()
		res := map[string]any{
			"ok":       err == nil,
			"command":  "ak " + strings.Join(fields, " "),
			"output":   string(out),
			"duration": time.Since(started).Round(time.Millisecond).String(),
		}
		if err != nil {
			res["error"] = err.Error()
			if ctx.Err() == context.DeadlineExceeded {
				res["error"] = "执行超时（20 分钟）"
			}
		}
		writeHTTPJSON(w, res)
	}))

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = srv.Serve(ln)
	}()
	fmt.Fprintf(os.Stderr, "android-kit web: %s\n", url)
	fmt.Fprintln(os.Stderr, "Ctrl-C 停止")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	return nil
}

func writeHTTPJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
