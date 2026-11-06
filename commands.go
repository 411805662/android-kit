package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (a *App) androidPath() string { return a.Paths.AndroidCLIPath(a.Config) }

func (a *App) engine() (string, error) {
	rt := detectRuntime(a.Paths, a.Config)
	prefer := a.Opts.Engine
	if prefer == "" {
		prefer = a.Config.Prefer
	}
	switch prefer {
	case "android":
		if !rt.HasAndroidCLI() {
			return "", fmt.Errorf("--engine=android 但找不到 android CLI（https://developer.android.com/cli）")
		}
		return "android", nil
	case "legacy":
		return "legacy", nil
	default:
		if rt.HasAndroidCLI() && versionAtLeast(rt.AndroidCLIversion, a.Config.AndroidCLI.MinVersion) {
			return "android", nil
		}
		return "legacy", nil
	}
}

func (p *Project) EnvList() []string {
	var out []string
	for k, v := range p.Env {
		out = append(out, k+"="+v)
	}
	return out
}

func (a *App) mustProject() (*Project, error) {
	return a.project(true)
}

func (a *App) device(proj *Project) string {
	return a.Paths.PickDevice(proj, a.Opts.Device)
}

func (a *App) adb(proj *Project, args ...string) error {
	adb, ok := lookPath("adb")
	if !ok {
		return fmt.Errorf("找不到 adb，请安装 Android SDK Platform-Tools 并加入 PATH")
	}
	if serial := a.device(proj); serial != "" {
		args = append([]string{"-s", serial}, args...)
	}
	dir := ""
	if proj != nil {
		dir = proj.Root
	}
	return runStream(dir, nil, adb, args...)
}

func (a *App) adbCapture(proj *Project, args ...string) (string, error) {
	adb, ok := lookPath("adb")
	if !ok {
		return "", fmt.Errorf("找不到 adb")
	}
	if serial := a.device(proj); serial != "" {
		args = append([]string{"-s", serial}, args...)
	}
	dir := ""
	if proj != nil {
		dir = proj.Root
	}
	return runCapture(dir, nil, adb, args...)
}

var reActivityRef = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.]*)/([A-Za-z0-9_.$]+)`)

// resumedActivity 读取设备当前前台 Activity，返回 com.pkg/.Act 形式；失败返回空串。
func (a *App) resumedActivity(proj *Project) string {
	if out, err := a.adbCapture(proj, "shell", "dumpsys", "activity", "activities"); err == nil {
		if act := pickActivity(out, "ResumedActivity"); act != "" {
			return act
		}
	}
	if out, err := a.adbCapture(proj, "shell", "dumpsys", "window", "displays"); err == nil {
		if act := pickActivity(out, "mCurrentFocus"); act != "" {
			return act
		}
	}
	return ""
}

func pickActivity(out, marker string) string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		if m := reActivityRef.FindStringSubmatch(line); m != nil {
			return normalizeActivityRef(m[1], m[2])
		}
	}
	return ""
}

func normalizeActivityRef(pkg, act string) string {
	if strings.HasPrefix(act, pkg+".") {
		act = "." + strings.TrimPrefix(act, pkg+".")
	}
	return pkg + "/" + act
}

// currentActivity 优先用设备实时前台 Activity，取不到时退回项目配置里的启动 Activity。
func (a *App) currentActivity(proj *Project) string {
	if proj == nil {
		return ""
	}
	if act := a.resumedActivity(proj); act != "" {
		return act
	}
	if proj.Package == "" || proj.Activity == "" {
		return ""
	}
	return normalizeActivityRef(proj.Package, proj.Activity)
}

func gradleTask(module, variant string) string {
	variant = strings.ToUpper(variant[:1]) + variant[1:]
	if module == "" || module == ":" {
		return "assemble" + variant
	}
	return module + ":assemble" + variant
}

// gradleInstallTask 是 Gradle 自带的安装任务，例如 :app:installDebug。
// 它依赖对应的 assemble 任务，能自己找对 APK（多 flavor / split 也不会装错），
// 并且直接走设备安装，比「先 build 再 adb install」少一次 Gradle 配置。
func gradleInstallTask(module, variant string) string {
	if variant == "" {
		variant = "debug"
	}
	variant = strings.ToUpper(variant[:1]) + variant[1:]
	if module == "" || module == ":" {
		return "install" + variant
	}
	return module + ":install" + variant
}

// gradleProjectRef 把配置里的模块名转成官方 CLI 认的 Gradle 项目路径（:app）。
// `android run` 收的是「项目路径」而不是任务名；返回空串表示让它自己从当前目录解析。
func gradleProjectRef(module string) string {
	module = strings.TrimSpace(module)
	if module == "" || module == ":" {
		return ""
	}
	if !strings.HasPrefix(module, ":") {
		module = ":" + module
	}
	return module
}

// androidInstallTask 是官方 CLI 用的安装任务引用：app:installDebug。
func androidInstallTask(module, variant string) string {
	if variant == "" {
		variant = "debug"
	}
	variant = strings.ToUpper(variant[:1]) + variant[1:]
	mod := strings.TrimPrefix(strings.TrimSpace(module), ":")
	if mod == "" || mod == "/" {
		return "install" + variant
	}
	return mod + ":install" + variant
}

func (a *App) gradle(proj *Project, tasks ...string) error {
	wrapper := a.Paths.GradleWrapper(proj)
	if wrapper == "" {
		return fmt.Errorf("项目里找不到 gradlew: %s", proj.Root)
	}
	return runStream(proj.Root, proj.EnvList(), wrapper, tasks...)
}

// gradleInstall 跑 install<Variant>，并把设备固定到当前选中的 serial，
// 避免 Gradle 默认往所有已连接设备都装一份。
func (a *App) gradleInstall(proj *Project) error {
	wrapper := a.Paths.GradleWrapper(proj)
	if wrapper == "" {
		return fmt.Errorf("项目里找不到 gradlew: %s", proj.Root)
	}
	env := proj.EnvList()
	if serial := a.device(proj); serial != "" {
		env = append(env, "ANDROID_SERIAL="+serial)
	}
	task := gradleInstallTask(proj.Module, proj.Variant)
	fmt.Fprintf(os.Stderr, "→ ./gradlew %s\n", task)
	return runStream(proj.Root, env, wrapper, task)
}

// androidBuildTask 用官方 CLI 跑一个 Gradle 任务，例如
// android build app:installDebug —— 官方 CLI 优先时的等价写法。
func (a *App) androidBuildTask(proj *Project, tasks ...string) error {
	env := proj.EnvList()
	if serial := a.device(proj); serial != "" {
		env = append(env, "ANDROID_SERIAL="+serial)
	}
	args := append([]string{"build"}, tasks...)
	fmt.Fprintf(os.Stderr, "→ android %s\n", strings.Join(args, " "))
	return runStream(proj.Root, env, a.androidPath(), args...)
}

func (a *App) build(proj *Project, clean bool, extra []string) error {
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if engine == "android" {
		variant := proj.Variant
		if variant == "" {
			variant = "debug"
		}
		target := strings.TrimPrefix(proj.Module, ":")
		if target == "" {
			target = "assemble" + strings.ToUpper(variant[:1]) + variant[1:]
		} else {
			target = target + ":assemble" + strings.ToUpper(variant[:1]) + variant[1:]
		}
		if clean {
			cleanTarget := "clean"
			if mod := strings.TrimPrefix(proj.Module, ":"); mod != "" {
				cleanTarget = mod + ":clean"
			}
			cleanArgs := []string{"build", "?" + cleanTarget}
			fmt.Fprintf(os.Stderr, "→ android %s\n", strings.Join(cleanArgs, " "))
			if err := runStream(proj.Root, proj.EnvList(), a.androidPath(), cleanArgs...); err != nil {
				return err
			}
		}
		args := []string{"build", "?" + target}
		args = append(args, extra...)
		fmt.Fprintf(os.Stderr, "→ android %s\n", strings.Join(args, " "))
		return runStream(proj.Root, proj.EnvList(), a.androidPath(), args...)
	}
	variant := proj.Variant
	if variant == "" {
		variant = "debug"
	}
	tasks := []string{}
	if clean {
		tasks = append(tasks, "clean")
	}
	tasks = append(tasks, gradleTask(proj.Module, variant))
	tasks = append(tasks, extra...)
	fmt.Fprintf(os.Stderr, "→ ./gradlew %s\n", strings.Join(tasks, " "))
	return a.gradle(proj, tasks...)
}

func (a *App) cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	clean := fs.Bool("clean", false, "先执行 clean")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	return a.build(proj, *clean, fs.Args())
}

// installAPK 把应用装到设备上。
// apk 为空且 allowBuild 为真时走 Gradle 自带的 install<Variant> 任务：
// 它会先 assemble 再安装，能自己找对 APK（多 flavor / split 也不会装错），
// 也不会出现「装了上一次构建的旧包」这种情况。
// 指定了 apk 就只安装那个文件（官方 CLI 可用时走 delta install）。
func (a *App) installAPK(proj *Project, apk string, allowBuild bool) error {
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if apk == "" && allowBuild && a.Paths.GradleWrapper(proj) != "" {
		if a.device(proj) == "" {
			return fmt.Errorf("没有连接的设备，先插上手机或启动模拟器（ak devices 查看）")
		}
		// 官方 CLI 可用时走 android build <模块>:install<Variant>，等价于
		// ./gradlew :app:installDebug，但由官方 CLI 统一管理 Gradle / JDK
		if engine == "android" {
			return a.androidBuildTask(proj, androidInstallTask(proj.Module, proj.Variant))
		}
		return a.gradleInstall(proj)
	}
	if apk == "" && engine == "android" {
		var err error
		apk, err = a.Store.APK(proj)
		if err != nil {
			return fmt.Errorf("%w\n\n先运行: ak build", err)
		}
	}
	if apk != "" && engine == "android" {
		args := []string{"install", apk}
		if dev := a.device(proj); dev != "" {
			args = append(args, "--device", dev)
		}
		fmt.Fprintf(os.Stderr, "→ android %s\n", strings.Join(args, " "))
		return runStream(proj.Root, proj.EnvList(), a.androidPath(), args...)
	}
	if apk == "" {
		apk, err = a.Store.APK(proj)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "→ adb install %s\n", relativize(proj.Root, apk))
	return a.adb(proj, "install", "-r", "-t", "-d", apk)
}

func (a *App) cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	apk := fs.String("apk", "", "指定 APK 路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	path := *apk
	if path != "" {
		path = expandHome(path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(proj.Root, path)
		}
	}
	// 指定了 APK 就只装这个文件；没指定时让 Gradle 构建并安装当前变体
	return a.installAPK(proj, path, path == "")
}

func (a *App) startApp(proj *Project) error {
	if proj.Package == "" {
		return fmt.Errorf("项目缺少 package，请运行 ak project add")
	}
	// monkey 一条命令就能拉起 launcher 默认 Activity，不用先解析
	err := a.adb(proj, "shell", "monkey", "-p", proj.Package, "1")
	if err == nil {
		return nil
	}
	if proj.Activity != "" {
		return a.adb(proj, "shell", "am", "start", "-n", proj.Package+"/"+proj.Activity)
	}
	return err
}

// launchTarget 描述 ak open 实际会启动什么。
func (a *App) launchTarget(proj *Project) string {
	if proj == nil || proj.Package == "" {
		return "-"
	}
	return "monkey -p " + proj.Package + " 1 (launcher 默认 Activity)"
}

func (a *App) cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	noBuild := fs.Bool("no-build", false, "不构建，直接安装现有 APK")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if engine == "android" && !*noBuild {
		// 官方 CLI 的 run 一步完成「构建 + delta 安装 + 启动」，比「先 build 再
		// 装 APK」快很多；传 Gradle 模块引用（:app）而不是 APK 路径，多模块 /
		// 动态特性 / ABI 分包的项目也不会装错或装到旧包。
		cmdArgs := []string{"run"}
		if ref := gradleProjectRef(proj.Module); ref != "" {
			cmdArgs = append(cmdArgs, ref)
		}
		if dev := a.device(proj); dev != "" {
			cmdArgs = append(cmdArgs, "--device", dev)
		}
		fmt.Fprintf(os.Stderr, "→ android %s\n", strings.Join(cmdArgs, " "))
		return runStream(proj.Root, proj.EnvList(), a.androidPath(), cmdArgs...)
	}
	if !*noBuild {
		// 走 gradlew 的 install<Variant> 时它自己会先 assemble，
		// 这里不再单独 build，省掉一次 Gradle 配置。
		if a.Paths.GradleWrapper(proj) != "" {
			if err := a.installAPK(proj, "", true); err != nil {
				return err
			}
			return a.startApp(proj)
		}
		if err := a.build(proj, false, nil); err != nil {
			return err
		}
	}
	if err := a.installAPK(proj, "", false); err != nil {
		return err
	}
	return a.startApp(proj)
}

func (a *App) cmdStop(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	return a.adb(proj, "shell", "am", "force-stop", proj.Package)
}

func (a *App) cmdOpen(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	return a.startApp(proj)
}

func (a *App) cmdRestart(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	if err := a.adb(proj, "shell", "am", "force-stop", proj.Package); err != nil {
		return err
	}
	return a.startApp(proj)
}

func (a *App) cmdClear(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	return a.adb(proj, "shell", "pm", "clear", proj.Package)
}

func (a *App) cmdAppUninstall(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	return a.adb(proj, "uninstall", proj.Package)
}

// localIPv4 返回本机第一个非回环 IPv4，用于把手机代理指向电脑。
func localIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return ip.String()
		}
	}
	return "127.0.0.1"
}

// cmdProxy 开关手机上的全局 HTTP 代理，等价于 px on / px off。
func (a *App) cmdProxy(args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	sub := "status"
	if len(rest) > 0 {
		sub = strings.ToLower(strings.TrimSpace(rest[0]))
	}
	proj, _ := a.project(false)
	switch sub {
	case "on", "set":
		port := "8888"
		if len(rest) > 1 && strings.TrimSpace(rest[1]) != "" {
			port = strings.TrimSpace(rest[1])
		}
		addr := localIPv4() + ":" + port
		if err := a.adb(proj, "shell", "settings", "put", "global", "http_proxy", addr); err != nil {
			return err
		}
		fmt.Printf("代理已开启: %s\n", addr)
		fmt.Println("关闭: ak proxy off")
		return nil
	case "off", "clear":
		// http_proxy 是 Android 实际使用的设置，https_proxy 顺手清掉旧脚本可能留下的残留。
		if err := a.adb(proj, "shell", "settings", "put", "global", "http_proxy", ":0"); err != nil {
			return err
		}
		_ = a.adb(proj, "shell", "settings", "put", "global", "https_proxy", ":0")
		fmt.Println("代理已关闭")
		return nil
	case "status", "get":
		out, err := a.adbCapture(proj, "shell", "settings", "get", "global", "http_proxy")
		if err != nil {
			return err
		}
		v := strings.TrimSpace(out)
		if v == "" || v == "null" || v == ":0" {
			fmt.Println("代理: 未设置")
			return nil
		}
		fmt.Printf("代理: %s\n", v)
		return nil
	default:
		return fmt.Errorf("用法: ak proxy on [端口] | ak proxy off | ak proxy status")
	}
}

func (a *App) cmdLogcat(args []string) error {
	fs := flag.NewFlagSet("logcat", flag.ContinueOnError)
	clear := fs.Bool("c", false, "先清空日志")
	grep := fs.String("grep", "", "只显示包含该字符串的日志")
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	if *clear {
		if _, err := a.adbCapture(proj, "logcat", "-c"); err != nil {
			return err
		}
	}
	pattern := strings.TrimSpace(*grep)
	if pattern == "" && len(positional) > 0 {
		pattern = strings.TrimSpace(strings.Join(positional, " "))
	}
	if pattern != "" {
		return a.logcatFiltered(proj, pattern)
	}
	pid := ""
	if proj.Package != "" {
		if out, err := a.adbCapture(proj, "shell", "pidof", proj.Package); err == nil {
			if fields := strings.Fields(out); len(fields) > 0 {
				pid = fields[0]
			}
		}
	}
	if pid != "" {
		return a.adb(proj, "logcat", "-v", "time", "--pid="+pid)
	}
	return a.logcatFiltered(proj, proj.Package)
}

func (a *App) logcatFiltered(proj *Project, pattern string) error {
	adb, ok := lookPath("adb")
	if !ok {
		return fmt.Errorf("找不到 adb")
	}
	pid := ""
	if proj != nil && proj.Package != "" {
		if out, err := a.adbCapture(proj, "shell", "pidof", proj.Package); err == nil {
			if fields := strings.Fields(out); len(fields) > 0 {
				pid = fields[0]
			}
		}
	}
	args := []string{"logcat", "-v", "time"}
	if pid != "" {
		args = append(args, "--pid="+pid)
	}
	if serial := a.device(proj); serial != "" {
		args = append([]string{"-s", serial}, args...)
	}
	cmd := exec.Command(adb, args...)
	cmd.Dir = proj.Root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	needle := strings.ToLower(pattern)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), needle) {
			fmt.Println(line)
		}
	}
	return cmd.Wait()
}

func (a *App) cmdLayout(args []string) error {
	fs := flag.NewFlagSet("layout", flag.ContinueOnError)
	pretty := fs.Bool("p", false, "格式化 JSON")
	prettyLong := fs.Bool("pretty", false, "格式化 JSON")
	full := fs.Bool("full", false, "包含不可交互节点")
	flat := fs.Bool("flat", false, "输出扁平列表")
	saveOnly := fs.Bool("save-only", false, "只保存，不打印内容")
	printBody := fs.Bool("print", false, "同时把 UI JSON 打印到终端")
	noShot := fs.Bool("no-shot", false, "不自动截图")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	_, visible, err := a.Paths.EnsureProjectDirs(proj)
	if err != nil {
		return err
	}
	stamp := timestamp()
	outPath := filepath.Join(a.uiDir(proj), stamp+".json")
	var content string
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if engine == "android" {
		cmdArgs := []string{"layout"}
		if *pretty || *prettyLong {
			cmdArgs = append(cmdArgs, "-p")
		}
		if *full {
			cmdArgs = append(cmdArgs, "--full")
		}
		if *flat {
			cmdArgs = append(cmdArgs, "--flat")
		}
		if dev := a.device(proj); dev != "" {
			cmdArgs = append(cmdArgs, "--device", dev)
		}
		content, err = runCapture(proj.Root, proj.EnvList(), a.androidPath(), cmdArgs...)
		if err != nil {
			return fmt.Errorf("android layout 失败: %s", content)
		}
	} else {
		remote := "/sdcard/android-kit-ui.xml"
		if _, err := a.adbCapture(proj, "shell", "uiautomator", "dump", remote); err != nil {
			return err
		}
		content, err = a.adbCapture(proj, "exec-out", "cat", remote)
		if err != nil {
			return err
		}
		outPath = strings.TrimSuffix(outPath, ".json") + ".xml"
	}
	if *pretty || *prettyLong {
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(content), "", "  ") == nil {
			content = buf.String()
		}
	}
	if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
		return err
	}
	if act := a.currentActivity(proj); act != "" {
		side := strings.TrimSuffix(outPath, filepath.Ext(outPath)) + ".activity"
		_ = os.WriteFile(side, []byte(act+"\n"), 0o644)
	}
	shotPath := ""
	if !*noShot {
		shotPath = filepath.Join(visible, "screenshots", stamp+".png")
		if err := os.MkdirAll(filepath.Dir(shotPath), 0o755); err != nil {
			return err
		}
		if captured, err := a.captureScreenshot(proj, shotPath, false); err == nil {
			shotPath = captured
		} else {
			shotPath = ""
			fmt.Fprintln(os.Stderr, "# shot failed: "+err.Error())
		}
	}
	fmt.Fprintln(os.Stderr, "# saved: "+outPath)
	if shotPath != "" {
		fmt.Fprintln(os.Stderr, "# shot: "+shotPath)
	}
	if viewer := a.viewerURL(proj, outPath, shotPath); viewer != "" {
		fmt.Fprintln(os.Stderr, "# viewer: "+viewer)
	} else {
		fmt.Fprintln(os.Stderr, "# hint: 运行 ak web 后可获得 H5 对照链接")
	}
	if !*saveOnly && *printBody {
		fmt.Println(content)
	}
	return nil
}

func (a *App) cmdScreenshot(args []string) error {
	fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
	output := fs.String("o", "", "输出路径")
	annotate := fs.Bool("a", false, "标注 UI 元素编号")
	annotateLong := fs.Bool("annotate", false, "标注 UI 元素编号")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	_, visible, err := a.Paths.EnsureProjectDirs(proj)
	if err != nil {
		return err
	}
	name := timestamp()
	if *annotate || *annotateLong {
		name += "-annotated"
	}
	path := *output
	if path == "" {
		path = filepath.Join(visible, "screenshots", name+".png")
	} else {
		path = expandHome(path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(proj.Root, path)
		}
		if isDir(path) {
			path = filepath.Join(path, name+".png")
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	path, err = a.captureScreenshot(proj, path, *annotate || *annotateLong)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func (a *App) captureScreenshot(proj *Project, path string, annotate bool) (string, error) {
	engine, err := a.engine()
	if err != nil {
		return "", err
	}
	if engine == "android" {
		cmdArgs := []string{"screen", "capture", "--output", path}
		if annotate {
			cmdArgs = append(cmdArgs, "--annotate")
		}
		if dev := a.device(proj); dev != "" {
			cmdArgs = append(cmdArgs, "--device", dev)
		}
		out, err := runCapture(proj.Root, proj.EnvList(), a.androidPath(), cmdArgs...)
		if err != nil {
			return "", fmt.Errorf("android screen capture 失败: %s", out)
		}
		if !exists(path) {
			if strings.Contains(out, ".png") {
				path = extractPNGPath(out)
			}
		}
	} else {
		adb, ok := lookPath("adb")
		if !ok {
			return "", fmt.Errorf("找不到 adb")
		}
		cmd := []string{"exec-out", "screencap", "-p"}
		if serial := a.device(proj); serial != "" {
			cmd = append([]string{"-s", serial}, cmd...)
		}
		data, err := execCaptureBytes(adb, cmd...)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
	}
	return path, nil
}

// viewerURL returns a ready-to-open H5 viewer link when `ak web` is running.
func (a *App) viewerURL(proj *Project, uiPath, shotPath string) string {
	baseBytes, err := os.ReadFile(a.Paths.WebURL)
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(string(baseBytes))
	if base == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	conn, err := net.DialTimeout("tcp", host, 300*time.Millisecond)
	if err != nil {
		return ""
	}
	_ = conn.Close()
	tokenBytes, err := os.ReadFile(a.Paths.WebToken)
	if err != nil {
		return ""
	}
	token := strings.TrimSpace(string(tokenBytes))
	q := url.Values{}
	q.Set("ui", uiPath)
	if shotPath != "" {
		q.Set("shot", shotPath)
	}
	if proj != nil {
		q.Set("project", proj.ID)
	}
	if token != "" {
		q.Set("token", token)
	}
	return base + "/viewer?" + q.Encode()
}

func extractPNGPath(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(strings.ToLower(line), ".png") && exists(line) {
			return line
		}
	}
	return out
}

func (a *App) cmdTap(args []string) error {
	fs := flag.NewFlagSet("tap", flag.ContinueOnError)
	wait := fs.String("wait", "", "等待元素出现（毫秒）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("用法: ak tap <x> <y>  或  ak tap \"text=登录\"")
	}
	if len(rest) >= 2 {
		if _, e1 := strconv.Atoi(rest[0]); e1 == nil {
			if _, e2 := strconv.Atoi(rest[1]); e2 == nil {
				return a.adb(proj, "shell", "input", "tap", rest[0], rest[1])
			}
		}
	}
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if engine != "android" {
		return fmt.Errorf("选择器点击需要官方 android CLI；坐标点击请用: ak tap 100 200")
	}
	cmdArgs := []string{"input", "tap", strings.Join(rest, " ")}
	if *wait != "" {
		cmdArgs = append(cmdArgs, "--wait", *wait)
	}
	if dev := a.device(proj); dev != "" {
		cmdArgs = append(cmdArgs, "--device", dev)
	}
	return runStream(proj.Root, proj.EnvList(), a.androidPath(), cmdArgs...)
}

func (a *App) cmdText(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: ak text \"hello world\"")
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	text := strings.Join(args, " ")
	text = strings.ReplaceAll(text, " ", "%s")
	return a.adb(proj, "shell", "input", "text", text)
}

func (a *App) cmdSwipe(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	if len(args) < 4 {
		return fmt.Errorf("用法: ak swipe <x1> <y1> <x2> <y2> [duration_ms]")
	}
	if len(args) >= 5 {
		return a.adb(proj, "shell", "input", "swipe", args[0], args[1], args[2], args[3], args[4])
	}
	return a.adb(proj, "shell", "input", "swipe", args[0], args[1], args[2], args[3])
}

func (a *App) cmdKey(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("用法: ak key <keycode>，例如 ak key 4")
	}
	return a.adb(proj, "shell", "input", "keyevent", args[0])
}

func (a *App) cmdPreview(args []string) error {
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if engine != "android" {
		return fmt.Errorf("Compose 预览需要官方 android CLI 1.1+")
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list":
		return runStream(proj.Root, proj.EnvList(), a.androidPath(), "preview", "list")
	case "render":
		_, visible, err := a.Paths.EnsureProjectDirs(proj)
		if err != nil {
			return err
		}
		outDir := filepath.Join(visible, "previews")
		cmdArgs := append([]string{"preview", "render", "--output", outDir}, args...)
		return runStream(proj.Root, proj.EnvList(), a.androidPath(), cmdArgs...)
	default:
		return fmt.Errorf("用法: ak preview list | ak preview render [--fqn=...]")
	}
}

func (a *App) cmdAnalyze(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: ak analyze <apk|aab|aar|jar>")
	}
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if engine != "android" {
		if apkAnalyzer, ok := lookPath("apkanalyzer"); ok {
			return runStream("", nil, apkAnalyzer, args...)
		}
		return fmt.Errorf("需要官方 android CLI 或 apkanalyzer")
	}
	return runStream("", nil, a.androidPath(), append([]string{"analyze"}, args...)...)
}

func (a *App) cmdDocs(args []string) error {
	engine, err := a.engine()
	if err != nil {
		return err
	}
	if engine != "android" {
		return fmt.Errorf("android docs 需要官方 android CLI")
	}
	if len(args) == 0 {
		args = []string{"search", "android"}
	} else if args[0] != "search" && args[0] != "fetch" {
		args = append([]string{"search"}, args...)
	}
	return runStream("", nil, a.androidPath(), append([]string{"docs"}, args...)...)
}

func (a *App) cmdDevices(args []string) error {
	devices := a.Paths.Devices()
	if a.Opts.JSON {
		return printJSON(devices)
	}
	if len(devices) == 0 {
		fmt.Println("没有已连接的设备。启动模拟器或用 USB 连接手机后重试。")
		return nil
	}
	for _, d := range devices {
		fmt.Println(d.Label())
	}
	return nil
}

func (a *App) cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	refresh := fs.Bool("refresh", false, "重新探测项目，刷新缓存的包名 / 模块 / APK 路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.project(false)
	if err != nil {
		return err
	}
	if *refresh && proj != nil {
		updated, err := a.Store.Refresh(proj)
		if err != nil {
			return err
		}
		proj = updated
	}
	rt := detectRuntime(a.Paths, a.Config)
	engine := "legacy"
	if rt.HasAndroidCLI() && versionAtLeast(rt.AndroidCLIversion, a.Config.AndroidCLI.MinVersion) {
		engine = "android"
	}
	devices := a.Paths.Devices()
	status := map[string]any{
		"project":        proj,
		"device":         a.device(proj),
		"devices":        devices,
		"engine":         engine,
		"android_cli":    rt.AndroidCLIversion,
		"adb":            rt.ADB,
		"paths":          a.Paths,
		"aliases_preset": a.Config.Aliases.Preset,
	}
	if proj != nil {
		if apk, err := a.Store.APK(proj); err == nil {
			status["apk"] = apk
		}
	}
	if a.Opts.JSON {
		return printJSON(status)
	}
	if proj == nil {
		fmt.Println("项目: (未选择)")
	} else {
		fmt.Printf("项目:   %s (%s)\n", proj.Name, proj.ID)
		fmt.Printf("路径:   %s\n", proj.Root)
		fmt.Printf("包名:   %s\n", orDash(proj.Package))
		fmt.Printf("启动:   %s\n", a.launchTarget(proj))
		fmt.Printf("模块:   %s   变体: %s\n", orDash(proj.Module), orDash(proj.Variant))
		if apk, err := a.Store.APK(proj); err == nil {
			fmt.Printf("APK:    %s\n", apk)
		} else {
			fmt.Printf("APK:    %v\n", err)
		}
	}
	fmt.Printf("设备:   %s\n", orDash(a.device(proj)))
	fmt.Printf("引擎:   %s (android %s)\n", engine, orDash(rt.AndroidCLIversion))
	fmt.Printf("别名:   %s\n", a.Config.Aliases.Preset)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (a *App) cmdDoctor(args []string) error {
	proj, _ := a.project(false)
	rt := detectRuntime(a.Paths, a.Config)
	type check struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
		Hint   string `json:"hint,omitempty"`
	}
	var checks []check
	add := func(name string, ok bool, detail, hint string) {
		checks = append(checks, check{Name: name, OK: ok, Detail: detail, Hint: hint})
	}
	_, adbOK := lookPath("adb")
	add("adb", adbOK, printable(rt.ADB), "安装 Android SDK Platform-Tools 并加入 PATH")
	add("android CLI", rt.HasAndroidCLI(), printable(rt.AndroidCLIversion), "https://developer.android.com/cli")
	if rt.HasAndroidCLI() {
		add("android CLI >= "+a.Config.AndroidCLI.MinVersion, versionAtLeast(rt.AndroidCLIversion, a.Config.AndroidCLI.MinVersion), rt.AndroidCLIversion, "运行 android update")
	}
	add("java", rt.Java != "", printable(rt.Java), "安装 JDK 17+ 或使用 Android Studio 的 JBR")
	if proj != nil {
		add("项目目录", isDir(proj.Root), proj.Root, "ak project add <path> 重新登记")
		add("gradlew", a.Paths.GradleWrapper(proj) != "", printable(a.Paths.GradleWrapper(proj)), "项目需要 Gradle Wrapper")
		add("package", proj.Package != "", orDash(proj.Package), "编辑 "+a.Paths.ProjectsDir+"/"+proj.ID+".toml")
		target := a.launchTarget(proj)
		add("启动 Activity", target != "-", target, "留空即可，ak open 会解析 launcher 默认 Activity")
		if _, err := a.Store.APK(proj); err == nil {
			add("APK", true, "found", "")
		} else {
			add("APK", false, "not built yet", "运行 ak build")
		}
	}
	devices := a.Paths.Devices()
	online := 0
	for _, d := range devices {
		if d.State == "device" {
			online++
		}
	}
	add("设备", online > 0, fmt.Sprintf("%d online / %d total", online, len(devices)), "连接手机或启动模拟器")
	configWritable := true
	if f, err := os.CreateTemp(a.Paths.ConfigDir, ".ak-write-*"); err == nil {
		f.Close()
		os.Remove(f.Name())
	} else {
		configWritable = false
	}
	add("配置目录可写", configWritable, a.Paths.ConfigDir, "检查目录权限或设置 ANDROID_KIT_CONFIG_DIR")
	shellBlock := false
	for _, rc := range []string{"~/.zshrc", "~/.bash_profile"} {
		data, err := os.ReadFile(expandHome(rc))
		if err == nil && strings.Contains(string(data), "# >>> android-kit >>>") {
			shellBlock = true
		}
	}
	add("Shell 集成", shellBlock, "rc block", "运行 ak setup")

	if a.Opts.JSON {
		return printJSON(checks)
	}
	for _, c := range checks {
		fmt.Printf("%s %-22s %s\n", boolMark(c.OK), c.Name, c.Detail)
		if !c.OK && c.Hint != "" {
			fmt.Printf("  → %s\n", c.Hint)
		}
	}
	return nil
}

func printable(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (a *App) cmdWhere(args []string) error {
	fs := flag.NewFlagSet("where", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	// ak where <项目> 用来给 shell 的目录快捷（cd <项目>）取路径，
	// 不带参数时还是当前目录对应的项目。
	if rest := fs.Args(); len(rest) > 0 {
		proj := a.Store.Get(rest[0])
		if proj == nil {
			return fmt.Errorf("没有登记过项目 %q（ak dev list 查看已有的目录快捷）", rest[0])
		}
		fmt.Println(proj.Root)
		return nil
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	fmt.Println(proj.Root)
	return nil
}

func (a *App) cmdClean(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	all := fs.Bool("all", false, "两个生成物目录全部清空")
	screenshots := fs.Bool("screenshots", false, "只清截图")
	older := fs.String("older-than", "", "只清 N 天前，例如 7d")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.mustProject()
	if err != nil {
		return err
	}
	hidden, visible, err := a.Paths.EnsureProjectDirs(proj)
	if err != nil {
		return err
	}
	targets := []string{filepath.Join(hidden, "tmp"), filepath.Join(hidden, "logs")}
	if *screenshots {
		targets = []string{filepath.Join(visible, "screenshots")}
	}
	if *all {
		targets = []string{hidden, visible}
	}
	var cutoff time.Time
	if *older != "" {
		dur, err := time.ParseDuration(*older)
		if err != nil {
			days, err2 := strconv.Atoi(strings.TrimSuffix(*older, "d"))
			if err2 != nil {
				return fmt.Errorf("--older-than 需要 7d 或 168h 格式")
			}
			dur = time.Duration(days) * 24 * time.Hour
		}
		cutoff = time.Now().Add(-dur)
	}
	for _, target := range targets {
		if !exists(target) {
			continue
		}
		if cutoff.IsZero() {
			if *all {
				if err := os.RemoveAll(target); err != nil {
					return err
				}
				fmt.Printf("removed %s\n", target)
			} else {
				entries, _ := os.ReadDir(target)
				for _, e := range entries {
					_ = os.RemoveAll(filepath.Join(target, e.Name()))
				}
				fmt.Printf("cleaned %s\n", target)
			}
			continue
		}
		_ = filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(path)
			}
			return nil
		})
		fmt.Printf("cleaned older than %s in %s\n", *older, target)
	}
	return nil
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
