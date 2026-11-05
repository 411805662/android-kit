# android-kit

终端里的 Android 开发命令中心。一个 `ak` 命令，把 Gradle、adb 和官方 Android CLI 串起来；项目在哪个目录、包名是什么，都从配置里解析，不需要改命令本身。

```bash
ak run          # 构建 + 安装 + 启动
ak screenshot   # 截图到 <project>/android-kit/screenshots
ak layout -p    # UI 树 JSON
ak web          # 本地网页配置台
```

## 安装

macOS / Linux：

```bash
curl -fsSL https://github.com/411805662/android-kit/releases/latest/download/install.sh | sh
```

或走 jsDelivr（国内网络通常更快；新 tag 可能有几分钟缓存）：

```bash
curl -fsSL https://cdn.jsdelivr.net/gh/411805662/android-kit@v0.1.4/install.sh | sh
```

安装器会：

1. 按 OS/arch 下载对应二进制，校验 SHA256。
2. 安装到 `~/.local/bin/android-kit`，并创建 `ak` 软链。
3. 写入带标记的 shell 集成块（zsh / bash）。
4. 创建配置目录和安装清单。

自定义安装位置：

```bash
ANDROID_KIT_PREFIX="$HOME/tools" \
ANDROID_KIT_ALIASES=classic \
curl -fsSL https://github.com/411805662/android-kit/releases/latest/download/install.sh | sh
```

Windows（PowerShell）：

```powershell
irm https://cdn.jsdelivr.net/gh/411805662/android-kit@main/install.ps1 | iex
```

## 快速开始

```bash
ak project add ~/dev/Android/MyApp --default
ak doctor
ak run
```

`ak project add` 会自动探测：

- Gradle 根目录（向上找 `settings.gradle(.kts)`）
- `applicationId` / `namespace`
- `MAIN` + `LAUNCHER` Activity（支持 `activity-alias` 的 `targetActivity`）
- 模块（`:app` 或 `:features:xxx`）
- APK 输出路径

多项目：

```bash
ak project add ~/dev/Android/AppA --id appa
ak project add ~/dev/Android/AppB --id appb
ak use appa
ak run                 # 默认项目
ak -p appb run         # 临时指定
ak project list
```

项目移动后只需要改 `root`，命令不用改：

```bash
ak project add ~/new/path/AppA --id appa
```

## 命令

| 命令 | 作用 |
|---|---|
| `ak build [--clean]` | 构建 Debug；官方 CLI 优先，失败可回退 Gradle |
| `ak install [--apk path]` | 安装 APK；官方 CLI 使用 delta install |
| `ak run` | 构建 + 安装 + 启动 |
| `ak stop` / `ak open` / `ak restart` | 杀进程 / 启动 / 重启 |
| `ak clear` | 清除应用数据 |
| `ak app-uninstall` | 卸载手机上的 App（不是本工具） |
| `ak logcat [-c]` | 按 pid 过滤日志 |
| `ak devices` | 设备列表 |
| `ak layout [-p] [--full]` | UI 树，保存到 `.android-kit/ui/` |
| `ak screenshot [-a]` | 截图，保存到 `android-kit/screenshots/` |
| `ak tap 100 200` | 坐标点击 |
| `ak tap "id=btn_login"` | 选择器点击（官方 CLI） |
| `ak text "hello world"` | 输入文本 |
| `ak swipe 500 1500 500 500` | 滑动 |
| `ak key 4` | 按键 |
| `ak preview list\|render` | Compose 预览 |
| `ak analyze <apk>` | APK / AAB / AAR 分析 |
| `ak docs search <query>` | 官方文档检索 |
| `ak status` | 当前项目 / 设备 / 引擎 / APK |
| `ak doctor` | 环境逐项诊断 |
| `ak web` | 本地网页配置台 |
| `ak paths` | 所有存储路径 |
| `ak clean [--all]` | 清理生成物 |
| `ak import <file\|dir>` | 从旧 `.bash_profile` / `android-toolkit` 导入 |
| `ak setup` | 安装 / 修复 shell 集成 |
| `ak uninstall [--purge]` | 卸载本工具 |

全局参数：

```bash
ak -p <project-id> ...        # 指定项目
ak -d <device-serial> ...     # 指定设备
ak --engine=android|legacy    # 强制引擎
ak --json ...                 # 机器可读输出
```

## 快捷别名

安装时默认不覆盖你已有的短别名。需要经典组合：

```bash
ak setup --aliases classic --force-aliases
```

| 别名 | 等价 |
|---|---|
| `b` | `ak build` |
| `cb` | `ak build --clean` |
| `in` | `ak install` |
| `ba` / `bio` | `ak run` |
| `k` | `ak stop` |
| `o` | `ak open` |
| `ko` | `ak restart` |
| `c` | `ak clear` |
| `un` | `ak app-uninstall` |
| `ss` | `ak screenshot` |
| `ui` | `ak layout -p` |
| `l` | `ak logcat` |
| `lc` | `ak logcat -c` |
| `dev` | `cd` 到当前项目根目录 |

说明：

- `in` 在 bash 里是保留字，无法作为命令使用；bash 用户请用 `ak install` 或自定义 `ins`。
- 同名 alias/function 默认跳过；`--force-aliases` 才会覆盖。
- 网页保存别名后，新终端立即生效；已打开的终端按一次回车，prompt hook 会自动刷新。

## 网页配置台

```bash
ak web
```

- 只监听 `127.0.0.1`，随机端口。
- 一次性 token 写入 `~/.local/state/android-kit/web.token`（0600）。
- 只有一个页面：一张表把「名称 / 快捷键 / ak 命令 / 实际命令」放在一起，按构建、启动、调试、其他分组。
- 内置命令标「内置」，实际命令由项目配置生成，只读；自定义行可自由编辑命令。
- 表格底部可加行：从尚未使用的 ak 命令里选，或选「自定义（空白）」。
- 每行可复制 `ak` 命令、复制实际命令、直接执行；执行结果在底部抽屉里显示。
- 项目设置、全局设置、存储路径都在同一页的折叠区里。
- 保存后 CLI 下一条命令立即生效；已打开的终端按一次回车，prompt hook 会自动刷新。
- 网页执行默认开启；可在「全局设置」里关闭 `allow_run_from_web`。

## 存储位置

`ak paths` 会打印当前机器上的真实路径。

| 内容 | 默认位置 |
|---|---|
| 二进制 | `~/.local/bin/android-kit` |
| `ak` 入口 | `~/.local/bin/ak` |
| 全局配置 | `~/.config/android-kit/config.toml` |
| 项目配置 | `~/.config/android-kit/projects/*.toml` |
| 数据目录 | `~/.local/share/android-kit/` |
| 安装清单 | `~/.local/share/android-kit/install-manifest.json` |
| 备份 | `~/.local/share/android-kit/backups/` |
| 状态 | `~/.local/state/android-kit/` |
| Web token | `~/.local/state/android-kit/web.token` |
| 日志 | `~/.local/state/android-kit/logs/` |
| 缓存 | `~/.cache/android-kit/` |

项目内生成物默认跟着项目走：

```text
<project>/
  .android-kit/                 # 机器读：UI dump / trace / heap / 日志 / tmp
    ui/
    traces/
    heaps/
    reports/
    logs/
    tmp/
  android-kit/                  # 人看：截图 / Compose 预览 / 报告
    screenshots/
    previews/
    reports/
```

两个目录首次创建时都会写入 `.gitignore`（内容 `*`），不会污染 git。需要提交某张截图时用 `git add -f`。

环境变量覆盖：

```text
ANDROID_KIT_HOME
ANDROID_KIT_BIN_DIR
ANDROID_KIT_CONFIG_DIR
ANDROID_KIT_DATA_DIR
ANDROID_KIT_STATE_DIR
ANDROID_KIT_CACHE_DIR
ANDROID_KIT_PROJECT
ANDROID_SERIAL
```

## 官方 Android CLI 优先

`ak` 不内置 Android CLI，只检测和调用：

```text
prefer=auto     官方 CLI 版本 >= min_version 时优先，否则回退 gradle/adb
prefer=android  只用官方 CLI，不具备则报错
prefer=legacy   只用 gradle + adb
```

单次覆盖：

```bash
ak run --engine=legacy
ak build --engine=android
```

`ak doctor` 会显示当前每一项走哪条路径。

## 卸载

```bash
ak uninstall --dry-run     # 只预览
ak uninstall               # 删除 shell 块、二进制、缓存、状态；保留配置
ak uninstall --purge       # 连配置和项目列表一起删除
```

卸载按 `install-manifest.json` 执行，只删除自己写入的标记块，不会用 `grep -v` 模糊删除整段 rc。

项目里的 `.android-kit/` 和 `android-kit/` 属于用户数据，卸载默认保留；需要时：

```bash
ak clean --all
```

## 从旧脚本迁移

```bash
ak import ~/dev/Android/demo/android-toolkit
ak import ~/.bash_profile
```

会识别：

```text
PROJECT_ROOT / APP_NAME / MAIN_ACTIVITY / DEBUG_APK_PATH
alias in=... / b=... / k=... / o=...
```

导入前不会修改原文件；只写 `~/.config/android-kit/projects/`。

## 开发

```bash
go build -o bin/android-kit .
./bin/android-kit version
./scripts/build-release.sh 0.1.0
```

发布：

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions 会构建 macOS / Linux / Windows 产物并上传 Release。

## License

MIT
