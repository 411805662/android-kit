package main

import (
	"fmt"
	"os"
	"strings"
)

type Options struct {
	Project string
	Device  string
	Engine  string
	JSON    bool
	Quiet   bool
}

type App struct {
	Paths  Paths
	Config Config
	Store  *ProjectStore
	Opts   Options
}

func newApp(opts Options) *App {
	p := ResolvePaths()
	_ = p.Ensure()
	cfg := LoadConfig(p)
	return &App{Paths: p, Config: cfg, Store: LoadProjects(p), Opts: opts}
}

func (a *App) reload() {
	a.Config = LoadConfig(a.Paths)
	a.Store = LoadProjects(a.Paths)
}

func (a *App) project(required bool) (*Project, error) {
	proj, err := a.Store.Resolve(a.Opts.Project)
	if err != nil {
		if err == errNoProject && !required {
			return nil, nil
		}
		return nil, fmt.Errorf("%w\n\n  先登记一个项目:  ak project add /path/to/project\n  查看已有项目:  ak project list", err)
	}
	return proj, nil
}

func parseGlobal(args []string) (Options, []string) {
	var opts Options
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(rest) > 0 {
			rest = append(rest, arg)
			continue
		}
		take := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case arg == "-p" || arg == "--project":
			opts.Project = take()
		case strings.HasPrefix(arg, "--project="):
			opts.Project = strings.TrimPrefix(arg, "--project=")
		case arg == "-d" || arg == "--device":
			opts.Device = take()
		case strings.HasPrefix(arg, "--device="):
			opts.Device = strings.TrimPrefix(arg, "--device=")
		case arg == "--engine":
			opts.Engine = take()
		case strings.HasPrefix(arg, "--engine="):
			opts.Engine = strings.TrimPrefix(arg, "--engine=")
		case arg == "--json":
			opts.JSON = true
		case arg == "-q" || arg == "--quiet":
			opts.Quiet = true
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest
}

func main() {
	opts, args := parseGlobal(os.Args[1:])
	if len(args) == 0 {
		printHelp()
		return
	}
	app := newApp(opts)
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		printHelp()
	case "version", "-V", "--version":
		fmt.Printf("%s %s\n", appName, appVersion)
	case "setup", "init":
		err = app.cmdSetup(rest)
	case "unset":
		err = app.cmdUninstall(rest)
	case "paths", "path":
		err = app.cmdPaths(rest)
	case "web", "ui":
		err = app.cmdWeb(rest)
	case "project", "projects":
		err = app.cmdProject(rest)
	case "use":
		err = app.cmdUse(rest)
	case "alias", "aliases":
		err = app.cmdAlias(rest)
	case "shell":
		err = app.cmdShell(rest)
	case "build", "b":
		err = app.cmdBuild(rest)
	case "install", "in":
		err = app.cmdInstall(rest)
	case "run", "bio":
		err = app.cmdRun(rest)
	case "stop", "k":
		err = app.cmdStop(rest)
	case "open", "o":
		err = app.cmdOpen(rest)
	case "restart", "ko":
		err = app.cmdRestart(rest)
	case "clear", "c":
		err = app.cmdClear(rest)
	case "uninstall", "app-uninstall", "un":
		err = app.cmdAppUninstall(rest)
	case "logcat", "lgc":
		err = app.cmdLogcat(rest)
	case "layout", "ui-dump":
		err = app.cmdLayout(rest)
	case "screenshot", "ss":
		err = app.cmdScreenshot(rest)
	case "ls", "files":
		err = app.cmdLs(rest)
	case "tree":
		err = app.cmdTree(rest)
	case "cat":
		err = app.cmdCat(rest)
	case "sp", "prefs":
		err = app.cmdSP(rest)
	case "dump", "pull":
		err = app.cmdDump(rest)
	case "push":
		err = app.cmdPush(rest)
	case "tap":
		err = app.cmdTap(rest)
	case "text":
		err = app.cmdText(rest)
	case "swipe", "scroll":
		err = app.cmdSwipe(rest)
	case "key":
		err = app.cmdKey(rest)
	case "preview":
		err = app.cmdPreview(rest)
	case "analyze":
		err = app.cmdAnalyze(rest)
	case "docs":
		err = app.cmdDocs(rest)
	case "devices":
		err = app.cmdDevices(rest)
	case "status":
		err = app.cmdStatus(rest)
	case "doctor":
		err = app.cmdDoctor(rest)
	case "where", "cd":
		err = app.cmdWhere(rest)
	case "dev", "devs":
		err = app.cmdDev(rest)
	case "clean":
		err = app.cmdClean(rest)
	case "import":
		err = app.cmdImport(rest)
	default:
		fmt.Fprintf(os.Stderr, "ak: unknown command %q\n\n", cmd)
		printHelp()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nak: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`android-kit (ak) — Android 开发终端工具箱

用法:
  ak [全局参数] <命令> [命令参数]

一键执行（快捷键）
  b       构建应用
  in      安装应用
  bio     构建 + 安装 + 启动
  k       杀应用
  o       启动应用
  ko      重启应用
  un      卸载应用
  ss      截图
  ui      UI 树
  lgc     应用日志

项目
  快捷键  命令                        说明
  -       ak web                      打开本地网页配置台
  -       ak devices                  设备列表
  -       ak status                   当前项目 / 设备 / 引擎状态
  -       ak doctor                   环境诊断

  目录即项目：cd 到项目目录直接执行命令，未登记的目录会自动识别并登记，
  包名、模块、APK 路径都从目录内容解析，不需要先手动添加项目。
  网页「项目」页的项目列表里可以直接给项目填快捷键（如 dev），敲一下就等于
  cd 到那个目录；还能增删项目、重新识别，并把某个项目设为全局默认
  （当前目录不属于任何项目时回退到它）。

构建 · 安装
  快捷键  命令                        说明
  b       ak build                    构建 Debug（官方 CLI 优先，可回退 gradle）
  cb      ak build --clean            清理后构建
  in      ak install                  安装 APK
  un      ak uninstall                卸载手机上的 App
  bio     ak run                      构建 + 安装 + 启动

应用控制
  快捷键  命令                        说明
  o       ak open                     启动应用
  k       ak stop                     杀进程
  ko      ak restart                  重启应用
  c       ak clear                    清除应用数据

调试 · 截图 · UI
  快捷键  命令                        说明
  ss      ak screenshot [-a]          截图到 <project>/android-kit/screenshots
  ui      ak layout -p                截图 + UI 树，输出 H5 对照链接
  lgc     ak logcat [关键词]          当前应用日志；关键词模糊匹配 tag / message

应用文件（默认操作 /data/data/<包名>，路径写相对值即可）
  快捷键  命令                        说明
  -       ak ls [目录]                列目录，修改时间最新在前
  -       ak tree [目录] [-L 2]       目录树
  -       ak cat <文件>               看文件内容，如 shared_prefs/x.xml
  -       ak sp [名称]                列出 / 查看 SharedPreferences
  -       ak dump <路径> [-o 本地]     文件或整个目录 dump 到 android-kit/dump
  -       ak push <本地文件> [目标]    推文件进应用目录

  外部目录加前缀 ext:，例如 ak ls ext:download

设备输入
  快捷键  命令                        说明
  -       ak tap 100 200              点击坐标
  -       ak tap "id=btn_login"       用选择器点击（需官方 android CLI）
  -       ak swipe 500 1500 500 500   滑动
  -       ak key 4                    发送按键
  -       ak text "hello world"       输入文本

其他
  快捷键  命令                        说明
  -       ak paths                    查看本工具所有存储路径
  -       ak preview list|render      Compose 预览
  -       ak analyze <apk>            APK 分析
  -       ak docs search <关键字>     官方文档检索
  -       ak clean [--all]            清理生成物
  -       ak dev list|add|rm          多项目目录快捷（dev / dev2 …一键切目录）
  -       ak alias list|set|rm        管理快捷命令
  -       ak import <文件|目录>        从旧 .bash_profile / android-toolkit 导入
  -       ak setup                    安装 Shell 集成
  -       ak unset [--purge]          卸载本工具

全局参数:
  -p, --project <id>          指定项目（也可用 AK_PROJECT）
  -d, --device <serial>       指定设备（也可用 ANDROID_SERIAL）
      --engine <auto|android|legacy>
      --json                  机器可读输出

示例:
  ak project add ~/dev/Android/MyApp --default
  ak run
  ak screenshot -a
  ak web

文档: https://github.com/411805662/android-kit
`)
}
