# android-kit

**一键执行 Android 开发日常操作。** 装好之后不用记 Gradle 任务、adb 命令、包名和 Activity，敲一两个字母就能跑：

| 快捷键 | 作用 |
|---|---|
| `b` | 构建应用 |
| `in` | 安装应用 |
| `bio` | 构建 + 安装 + 启动 |
| `k` | 杀应用 |
| `o` | 启动应用 |
| `ko` | 重启应用 |
| `un` | 卸载应用 |
| `ss` | 截图 |
| `ui` | UI 树 |
| `lgc` | 应用日志 |

```bash
bio      # 构建 + 安装 + 启动：日常部署一条命令
k        # 杀进程
o        # 启动
ss       # 截图
ui       # 截图 + UI 树，输出 H5 对照链接
ak sp    # 看应用 SharedPreferences
ak ls    # 应用私有目录，改动最新的在前
```

**目录即项目**：cd 到项目目录直接敲命令就行，包名、模块、APK 路径都从目录内容解析；启动应用不需要知道 Activity，`ak open` 会让设备自己解析 launcher 默认 Activity。换项目只要 cd，命令本身不用动。底层优先调用官方 Android CLI，没有则回退 Gradle + adb。

`ak` 是完整命令入口，`ak web` 打开本地网页配置台。

## 安装

macOS / Linux：

```bash
curl -fsSL https://github.com/411805662/android-kit/releases/latest/download/install.sh | sh
```

国内网络可走 jsDelivr：

```bash
curl -fsSL https://cdn.jsdelivr.net/gh/411805662/android-kit@main/install.sh | sh
```

Windows（PowerShell）：

```powershell
irm https://cdn.jsdelivr.net/gh/411805662/android-kit@main/install.ps1 | iex
```

安装器按 OS / arch 下载对应二进制（校验 SHA256）装到 `~/.local/bin`，并写入 zsh / bash 的 shell 集成块；可用 `ANDROID_KIT_PREFIX` 改安装位置。

## 快速开始

```bash
cd ~/dev/Android/MyApp   # 进到项目目录，第一次会自动登记
ak doctor                # 检查 adb / CLI / Java / 项目
ak run                   # 构建 + 安装 + 启动
```

项目名取目录名，包名从 `build.gradle` / `AndroidManifest.xml` 读取，都不用填。多个项目之间切换就是 cd 到另一个目录，`ak status` 随时能看到当前生效的是哪个项目。

包名只在第一次登记时读一次，之后记在 `~/.android-kit/projects/<id>.toml` 里直接用，日常命令不再扫 Gradle 文件。改了 `applicationId` 之后用 `ak status --refresh`（或网页上的「重新识别」）刷新一次即可。

多项目：

```bash
cd ~/dev/Android/AppB    # 换目录即换项目
ak -p appb run           # 也可以临时指定项目
```

自动登记可以用 `AK_NO_AUTO_REGISTER=1` 关掉，此时只会使用已登记的项目。

## 命令

### 项目

| 快捷键 | 命令 | 作用 |
|---|---|---|
| | `ak web` | 打开本地网页配置台 |
| `dev` | `ak cd` | 进入当前项目根目录 |
| | `ak devices` | 设备列表 |
| | `ak status` | 当前项目 / 设备 / 引擎 / APK |
| | `ak doctor` | 环境逐项诊断 |

不需要手动登记项目：cd 到项目目录执行任意命令时会自动识别并登记。项目管理子命令（`ak project …`、`ak use`）暂时不在文档和网页里露出，功能仍然可用。

工程放在仓库子目录里（如 `<repo>/android/settings.gradle`）也能识别：当前目录下只有一个 Gradle 工程时会自动用它，并用仓库目录名作为项目名。

网页「项目」tab 有一块独立的项目列表：可以按目录添加、移除（只删配置不动文件）、重新识别，鼠标移到项目名后面还能「设为全局默认」。设了全局默认后，在不是任何项目的目录里执行 `ak` 会回退到它，不用每次都 `cd` 回项目。

### 多项目与目录快捷

cd 到哪个项目，`ak` 就作用于哪个项目。想少打几个字就建目录快捷：

```bash
ak dev add pokekara2       # 自动起名 dev2（dev 已被占用时顺延）
ak dev add cleaner pk      # 也可以自己指定名字
ak dev list                # 列出现有快捷
ak dev rm pk               # 删掉
```

每个快捷名都会生成一个 shell 函数，直接切目录：

```bash
dev     # 当前项目根目录（跟随所在目录）
dev2    # → /Users/…/PokeKara2
dev3    # → /Users/…/cleaner
```

网页里也能改：「项目」tab 每个已登记项目一行，在「快捷键」列填名字即可，自动保存、新终端生效。

### 构建 · 安装

| 快捷键 | 命令 | 作用 |
|---|---|---|
| `b` | `ak build` | 构建 Debug（`assembleDebug`） |
| `cb` | `ak build --clean` | 清理后构建（`clean` + `assembleDebug`） |
| `in` | `ak install` | 构建并安装（Gradle `install<Variant>`，如 `:app:installDebug`） |
| `un` | `ak uninstall` | 卸载手机上的 App |
| `bio` | `ak run` | 构建 + 安装 + 启动（官方 CLI `android run`，delta 安装只推差异） |

安装优先走 Gradle 自带的 `install<Variant>` 任务：它会先 `assemble` 再安装，自动找对 APK（多 flavor / split 也不会装错），比「先 build 再 `adb install`」少一次 Gradle 配置。指定了 APK（`ak install --apk <文件>`）时只安装该文件，不再构建。

`ak run` 走官方 CLI 的 `android run <模块>`：一条命令完成构建 + delta 安装 + 启动，只传输差异部分（大项目通常从几百 MB 降到几 MB）。传模块引用而不是 APK 路径，多模块 / 动态特性 / ABI 分包的项目也不会装错或装到旧包。

### 应用控制

| 快捷键 | 命令 | 作用 |
|---|---|---|
| `o` | `ak open` | 启动应用（设备解析 launcher 默认 Activity，不用配 Activity） |
| `k` | `ak stop` | 杀进程 |
| `ko` | `ak restart` | 重启应用 |
| `c` | `ak clear` | 清除应用数据 |

### 调试 · 截图 · UI

| 快捷键 | 命令 | 作用 |
|---|---|---|
| `ss` | `ak screenshot [-a]` | 截图到 `<project>/android-kit/screenshots` |
| `ui` | `ak layout -p` | 截图 + UI 树，输出 H5 对照链接 |
| `lgc` | `ak logcat [关键词]` | 当前应用日志，关键词模糊匹配 tag / message |

### 应用文件

默认操作应用私有目录 `/data/data/<包名>`，路径写相对值就行，不用每次拼一整条前缀：

| 快捷键 | 命令 | 作用 |
|---|---|---|
| | `ak ls [目录]` | 列目录，按修改时间最新在前 |
| | `ak tree [目录] [-L 2]` | 目录树，默认两层 |
| | `ak cat <文件>` | 看文件内容，如 `shared_prefs/settings.xml` |
| | `ak sp` / `ak sp <名称>` | 列出全部 SharedPreferences / 直接看某个的内容 |
| | `ak dump <路径> [-o 本地路径]` | 文件或整个目录拉到 `<project>/android-kit/dump` |
| | `ak push <本地文件> [目标]` | 推文件进应用目录，缺省是 `files/<文件名>` |

```bash
ak ls                          # /data/data/<包名>，最新改动在前
ak ls shared_prefs             # 相对路径
ak cat shared_prefs/settings.xml
ak sp settings                 # 自动补 .xml 后打印内容
ak dump files/.poke_log        # 整个目录按 tar 流拉下来
ak push ./mock.json files/mock.json
ak ls ext:                     # 外部目录 /sdcard/Android/data/<包名>/files
ak dump ext:download -o ~/Desktop/dl
```

私有目录走 `run-as`，要求 debug 包（`debuggable=true`）。外部目录用 `ext:` 前缀或直接写 `/sdcard/...`。

### 设备输入

| 快捷键 | 命令 | 作用 |
|---|---|---|
| `pt` | `ak text "hello world"` | 输入文本 |
| | `ak tap 100 200` | 坐标点击 |
| | `ak swipe 500 1500 500 500` | 滑动 |
| | `ak key 4` | 发送按键 |

### 其他

| 快捷键 | 命令 | 作用 |
|---|---|---|
| | `ak paths` | 查看本工具所有存储路径 |
| | `ak preview list\|render` | Compose 预览 |
| | `ak analyze <apk>` | APK 分析 |
| | `ak docs search <关键字>` | 官方文档检索 |
| | `ak clean [--all]` | 清理生成物 |
| | `ak alias list\|set\|rm` | 管理快捷命令 |
| | `ak import <文件\|目录>` | 从旧 `.bash_profile` / `android-toolkit` 导入 |
| | `ak setup` | 安装 / 修复 shell 集成 |
| | `ak unset [--purge]` | 卸载本工具 |

全局参数：

```bash
ak -p <project-id> ...         # 指定项目
ak -d <device-serial> ...      # 指定设备
ak --engine=android|legacy ... # 强制引擎
ak --json ...                  # 机器可读输出
```

## 快捷键

- 网页里改：`ak web` →「快捷键」列直接输入，自动保存；新终端立即生效，已打开的终端按一次回车即可。
- 命令行改：`ak alias list`，`ak alias set bb "build --clean"`，`ak alias rm bb`。
- 默认不覆盖你已有的同名 alias / function；`ak setup --aliases classic --force-aliases` 强制覆盖。
- `in` 在 bash 里是保留字，bash 用户请用 `ak install`，或换成别的名字。

## 网页配置台

```bash
ak web
```

- 只监听 `127.0.0.1` 随机端口，带一次性 token（写入 `~/.android-kit/web.token`，0600）。
- 三个 tab：**项目**（项目命令、项目列表、全局设置、存储路径）、**命令**（构建 · 安装 / 应用控制 / 调试 / 应用文件 / 设备输入 / 其他）、**UI 截图**（产物画廊）。
- **项目** tab 每个已登记项目一行「进入 <项目>」，当前项目带「当前」标记；在「快捷键」列填个名字就是目录快捷。
- **项目列表**单独一栏：可添加目录、移除（只删配置）、重新识别；鼠标移到项目名后可「设为全局默认」，非项目目录下执行 `ak` 会回退到它。
- **UI 截图** tab 按项目分组展示，UI 树和截图配对预览；产物存在 `~/.android-kit/artifacts/<项目>/ui`，截图存在 `<项目>/android-kit/screenshots`。
- **添加项目只填根目录**，名称取目录名，包名 / 模块 / 变体自动识别；需要纠正时点该行的「重新识别」。
- 表格列顺序统一为：名称 / ak 命令 / 快捷键 / 操作 / 实际命令。内置命令的实际命令由项目配置生成，只读；自定义行可自由编辑。
- 表格底部可以加行：从还没用过的 ak 命令里挑，或选「自定义（空白）」自己写一条。
- 每行可执行、复制 `ak` 命令、复制实际命令；执行结果在底部抽屉里显示。
- 「UI 截图」画廊按缩略图展示，可单独删除或清空；UI 树卡片可打开 H5 对照查看器，或看格式化后的 JSON。
- H5 对照查看器：左截图 / 右线框图 1:1 对齐，可搜索 `id`、`text`、`content-desc`、`class`；悬停线框看元素，点选后右侧展示完整属性（含 px / dp 换算）和当前页面 Activity；背景容器不挡点击，可隐藏元素并一键恢复；中间分隔条可拖动。
- 配置台、H5 查看器、JSON 页都支持黑白主题切换（右上角 🌙 / ☀️），选择保存在 `~/.android-kit/ui-theme`，换端口、重启都还在；没选过则跟随系统。
- `ak layout` 会自动截一张同时间戳的图，并输出 H5 链接。

## 存储位置

`ak paths` 会打印当前机器上的真实路径。

| 内容 | 位置 |
|---|---|
| 二进制 / `ak` 入口 | `~/.local/bin/android-kit`、`~/.local/bin/ak` |
| 全局配置 | `~/.android-kit/config.toml` |
| 项目配置 | `~/.android-kit/projects/*.toml` |
| UI 树等历史产物 | `~/.android-kit/artifacts/` |
| 备份 / 缓存 / 日志 / token | `~/.android-kit/` |
| 网页主题选择 | `~/.android-kit/ui-theme` |
| 需要人工查看的产物 | `<project>/android-kit/`（`screenshots`、`previews`、`reports`、`dump`） |

需要人工查看的截图、预览、报告放在项目目录，其余历史数据集中在用户目录，不跟单个项目绑定。项目内目录首次创建会写入 `.gitignore`（内容 `*`），不会污染 git；要提交某张截图用 `git add -f`。

## 引擎

`ak` 不内置 Android CLI，只做检测和调用：

```text
prefer=auto     官方 CLI 版本 >= min_version 时优先，否则回退 gradle / adb
prefer=android  只用官方 CLI，不具备则报错
prefer=legacy   只用 gradle + adb
```

单次覆盖用 `ak run --engine=legacy`；`ak doctor` 会显示每一项实际走哪条路径。

## 卸载

```bash
ak unset --dry-run     # 只预览
ak unset               # 删除 shell 块、二进制、缓存、状态；保留配置
ak unset --purge       # 连配置和项目列表一起删除
```

卸载按 `install-manifest.json` 执行，只删除自己写入的标记块。项目里的 `android-kit/` 属于用户数据，卸载默认保留，需要时用 `ak clean --all`。

## 开发

```bash
go build -o bin/android-kit .
./scripts/build-release.sh 0.2.23
```

推送 `v*` tag 会触发 GitHub Actions 构建 macOS / Linux / Windows 产物并上传 Release。

## License

MIT
