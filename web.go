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
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

type CommandInfo struct {
	ID        string   `json:"id"`
	Group     string   `json:"group"`
	Title     string   `json:"title"`
	Desc      string   `json:"desc"`
	Command   string   `json:"command"`
	Engine    string   `json:"engine"`
	Available bool     `json:"available"`
	Aliases   []string `json:"aliases"`
	Examples  []string `json:"examples"`
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
	ProjectDirs map[string]string `json:"project_dirs"`
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
		Commands:    a.commandCatalog(proj, engine),
		ProjectDirs: dirs,
	}
}

func (a *App) commandCatalog(proj *Project, engine string) []CommandInfo {
	aliasMap := map[string][]string{}
	for name, value := range a.Config.Aliases.Items {
		first := strings.Fields(value)
		if len(first) == 0 {
			continue
		}
		aliasMap[first[0]] = append(aliasMap[first[0]], name)
	}
	root := "<project>"
	pkg := "<package>"
	act := "<activity>"
	module := ":app"
	variant := "debug"
	apk := "<apk>"
	dev := ""
	if proj != nil {
		root = proj.Root
		pkg = orDash(proj.Package)
		act = orDash(proj.Activity)
		module = proj.Module
		variant = proj.Variant
		if p, err := a.Store.APK(proj); err == nil {
			apk = p
		}
		dev = a.device(proj)
	}
	devFlag := ""
	if dev != "" {
		devFlag = " --device " + dev
	}
	buildCmd := "./gradlew " + gradleTask(module, variant)
	installCmd := "adb install -r -t -d " + apk
	runCmd := buildCmd + " && " + installCmd + " && adb shell am start -n " + pkg + "/" + act
	if engine == "android" {
		buildCmd = "android build " + module
		installCmd = "android install " + module + devFlag
		runCmd = "android run " + module + " --activity " + act + devFlag
	}
	items := []CommandInfo{
		{ID: "build", Group: "构建", Title: "构建 Debug", Desc: "官方 android CLI 优先，失败可回退 Gradle", Command: buildCmd, Engine: engine, Available: true, Examples: []string{"ak build", "ak build --clean"}},
		{ID: "install", Group: "构建", Title: "安装 APK", Desc: "自动找最新 APK；官方 CLI 支持 delta install", Command: installCmd, Engine: engine, Available: true, Examples: []string{"ak install", "ak install --apk app/build/outputs/apk/debug/app-debug.apk"}},
		{ID: "run", Group: "构建", Title: "构建 + 安装 + 启动", Desc: "一条命令完成日常部署", Command: runCmd, Engine: engine, Available: true, Examples: []string{"ak run", "ak run --engine=legacy"}},
		{ID: "stop", Group: "应用", Title: "杀进程", Desc: "force-stop 当前包名", Command: "adb shell am force-stop " + pkg, Engine: "adb", Available: true, Examples: []string{"ak stop"}},
		{ID: "open", Group: "应用", Title: "启动应用", Desc: "有 Activity 用 am start，否则用 monkey", Command: "adb shell am start -n " + pkg + "/" + act, Engine: "adb", Available: true, Examples: []string{"ak open"}},
		{ID: "restart", Group: "应用", Title: "重启应用", Desc: "杀进程后立即启动", Command: "adb shell am force-stop " + pkg + " && adb shell am start -n " + pkg + "/" + act, Engine: "adb", Available: true, Examples: []string{"ak restart"}},
		{ID: "clear", Group: "应用", Title: "清除数据", Desc: "pm clear，登录态会丢失", Command: "adb shell pm clear " + pkg, Engine: "adb", Available: true, Examples: []string{"ak clear"}},
		{ID: "app-uninstall", Group: "应用", Title: "卸载 App", Desc: "卸载手机上的应用，不是本工具", Command: "adb uninstall " + pkg, Engine: "adb", Available: true, Examples: []string{"ak app-uninstall"}},
		{ID: "logcat", Group: "调试", Title: "应用日志", Desc: "按 pid 过滤；-c 先清空", Command: "adb logcat -v time --pid=$(adb shell pidof " + pkg + ")", Engine: "adb", Available: true, Examples: []string{"ak logcat", "ak logcat -c"}},
		{ID: "layout", Group: "调试", Title: "UI 树", Desc: "保存 JSON 到 .android-kit/ui，同时打印", Command: "android layout -p" + devFlag, Engine: engine, Available: true, Examples: []string{"ak layout", "ak layout -p --full"}},
		{ID: "screenshot", Group: "调试", Title: "截图", Desc: "保存到 android-kit/screenshots", Command: "android screen capture --output " + root + "/android-kit/screenshots/<time>.png" + devFlag, Engine: engine, Available: true, Examples: []string{"ak screenshot", "ak screenshot -a"}},
		{ID: "tap", Group: "调试", Title: "点击", Desc: "坐标或选择器", Command: "adb shell input tap 100 200", Engine: "adb", Available: true, Examples: []string{"ak tap 100 200", "ak tap \"id=btn_login\""}},
		{ID: "text", Group: "调试", Title: "输入文本", Desc: "空格自动转 %s", Command: "adb shell input text hello%sworld", Engine: "adb", Available: true, Examples: []string{"ak text \"hello world\""}},
		{ID: "swipe", Group: "调试", Title: "滑动", Desc: "四个坐标 + 可选时长", Command: "adb shell input swipe 500 1500 500 500", Engine: "adb", Available: true, Examples: []string{"ak swipe 500 1500 500 500 300"}},
		{ID: "preview", Group: "调试", Title: "Compose 预览", Desc: "无设备渲染 @Preview 为 PNG", Command: "android preview render --output " + root + "/android-kit/previews", Engine: "android", Available: engine == "android", Examples: []string{"ak preview list", "ak preview render --fqn=com.example.Preview"}},
		{ID: "analyze", Group: "调试", Title: "APK 分析", Desc: "DEX / R8 / keep 规则 / native 库", Command: "android analyze " + apk, Engine: "android", Available: engine == "android", Examples: []string{"ak analyze app-debug.apk"}},
		{ID: "docs", Group: "调试", Title: "官方文档", Desc: "搜索 Android Knowledge Base", Command: "android docs search <query>", Engine: "android", Available: engine == "android", Examples: []string{"ak docs search compose"}},
		{ID: "devices", Group: "环境", Title: "设备列表", Desc: "adb devices -l", Command: "adb devices -l", Engine: "adb", Available: true, Examples: []string{"ak devices"}},
		{ID: "status", Group: "环境", Title: "状态", Desc: "项目、设备、引擎、APK", Command: "ak status", Engine: "-", Available: true, Examples: []string{"ak status"}},
		{ID: "doctor", Group: "环境", Title: "环境诊断", Desc: "逐项检查 adb / CLI / Java / 项目", Command: "ak doctor", Engine: "-", Available: true, Examples: []string{"ak doctor"}},
		{ID: "web", Group: "环境", Title: "网页配置台", Desc: "本地 127.0.0.1，token 保护", Command: "ak web", Engine: "-", Available: true, Examples: []string{"ak web"}},
		{ID: "paths", Group: "环境", Title: "存储路径", Desc: "查看所有目录和文件位置", Command: "ak paths", Engine: "-", Available: true, Examples: []string{"ak paths"}},
		{ID: "clean", Group: "环境", Title: "清理生成物", Desc: "默认清 tmp/logs", Command: "ak clean", Engine: "-", Available: true, Examples: []string{"ak clean --screenshots", "ak clean --all"}},
	}
	for i := range items {
		items[i].Aliases = aliasMap[items[i].ID]
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
		data, err := webFS.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
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
	mux.HandleFunc("/api/run", auth(func(w http.ResponseWriter, r *http.Request) {
		if !a.Config.UI.AllowRunFromWeb {
			http.Error(w, "网页执行已关闭（Settings → allow_run_from_web）", 403)
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
		allowed := map[string]bool{"build": true, "install": true, "run": true, "stop": true, "open": true, "restart": true, "clear": true, "screenshot": true, "layout": true, "logcat": false}
		if !allowed[body.Command] {
			http.Error(w, "command not allowed", 403)
			return
		}
		writeHTTPJSON(w, map[string]any{"ok": true, "note": "请在终端执行: ak " + body.Command})
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
