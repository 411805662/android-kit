package main

import (
	"archive/tar"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 应用文件命令的路径规则：
//
//	shared_prefs/app.xml      相对路径，基于 /data/data/<包名>
//	/data/data/<包名>/files   绝对路径，直接使用（私有目录走 run-as）
//	ext:download/a.zip        外部目录，基于 /sdcard/Android/data/<包名>/files
//	/sdcard/...               外部目录绝对路径，不走 run-as
//
// 这样日常只需要写相对路径，不用每次拼一整条 /data/data/... 前缀。

func (a *App) resolveRemote(proj *Project, p string) (string, bool) {
	p = strings.TrimSpace(p)
	base := "/data/data/" + proj.Package
	extBase := "/sdcard/Android/data/" + proj.Package + "/files"
	switch {
	case strings.HasPrefix(p, "ext:"):
		return cleanRemotePath(path.Join(extBase, strings.TrimPrefix(p, "ext:"))), true
	case strings.HasPrefix(p, "/sdcard"), strings.HasPrefix(p, "/storage"), strings.HasPrefix(p, "/mnt/sdcard"):
		return cleanRemotePath(p), true
	case strings.HasPrefix(p, "/"):
		return cleanRemotePath(p), false
	case p == "" || p == "." || p == "~":
		return base, false
	default:
		return cleanRemotePath(path.Join(base, p)), false
	}
}

// fileProject 取当前项目，并确认包名已经识别过——没有包名就拼不出应用目录。
func (a *App) fileProject() (*Project, error) {
	proj, err := a.mustProject()
	if err != nil {
		return nil, err
	}
	if proj.Package == "" {
		return nil, fmt.Errorf("项目 %s 还没识别到包名\n\n  在网页「项目」页点一次「重新识别」，或先跑 ak build / ak doctor", proj.ID)
	}
	return proj, nil
}

func cleanRemotePath(p string) string {
	if p == "" {
		return p
	}
	clean := path.Clean(p)
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	return clean
}

// deviceScript 把 argv 拼成一条在设备上执行的 shell 命令。
// 私有目录自动套一层 run-as，外部目录直接执行。
func deviceScript(proj *Project, external bool, argv ...string) string {
	parts := make([]string, 0, len(argv))
	for _, s := range argv {
		parts = append(parts, shellQuote(s))
	}
	script := strings.Join(parts, " ")
	if external {
		return script
	}
	return "run-as " + shellQuote(proj.Package) + " " + script
}

func (a *App) runOnDevice(proj *Project, external bool, argv ...string) (string, error) {
	return a.adbCapture(proj, "shell", deviceScript(proj, external, argv...))
}

// execOut 走 adb exec-out，二进制安全，用来取文件内容或 tar 流。
func (a *App) execOut(proj *Project, script string) *exec.Cmd {
	adb, _ := lookPath("adb")
	args := []string{}
	if serial := a.device(proj); serial != "" {
		args = append(args, "-s", serial)
	}
	args = append(args, "exec-out", script)
	return exec.Command(adb, args...)
}

func (a *App) captureOut(proj *Project, script string) ([]byte, error) {
	cmd := a.execOut(proj, script)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *App) remoteKind(proj *Project, external bool, abs string) (string, error) {
	out, err := a.runOnDevice(proj, external, "ls", "-ld", abs)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if line == "" {
		return "", fmt.Errorf("路径不存在: %s", abs)
	}
	switch line[0] {
	case 'd':
		return "dir", nil
	case 'l':
		return "link", nil
	default:
		return "file", nil
	}
}

// adbArgs 拼出带设备选择的 adb 前缀参数。
func (a *App) adbArgs(proj *Project, rest ...string) []string {
	args := []string{}
	if serial := a.device(proj); serial != "" {
		args = append(args, "-s", serial)
	}
	return append(args, rest...)
}

type lsEntry struct {
	Perm string
	Size string
	When string
	Name string
	Dir  bool
}

func parseLsLong(out string) []lsEntry {
	var rows []lsEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "total ") || strings.HasSuffix(trimmed, ":") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		name := strings.Join(f[7:], " ")
		rows = append(rows, lsEntry{
			Perm: f[0],
			Size: f[4],
			When: f[5] + " " + f[6],
			Name: name,
			Dir:  strings.HasPrefix(f[0], "d"),
		})
	}
	return rows
}

func humanSize(s string) string {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s
	}
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func printLs(abs string, rows []lsEntry) {
	fmt.Printf("%s  (%d 项，最新在前)\n\n", abs, len(rows))
	fmt.Printf("  %-4s %10s  %-12s  %s\n", "类型", "大小", "修改时间", "名称")
	for _, r := range rows {
		kind := "文件"
		name := r.Name
		size := humanSize(r.Size)
		if r.Dir {
			kind = "目录"
			name += "/"
			size = "-"
		}
		fmt.Printf("  %-4s %10s  %-12s  %s\n", kind, size, r.When, name)
	}
}

func (a *App) cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	all := fs.Bool("a", false, "包含隐藏文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.fileProject()
	if err != nil {
		return err
	}
	target := ""
	if rest := fs.Args(); len(rest) > 0 {
		target = rest[0]
	}
	abs, external := a.resolveRemote(proj, target)
	listArgs := []string{"ls", "-lt"}
	if *all {
		listArgs = append(listArgs, "-A")
	}
	listArgs = append(listArgs, abs)
	out, err := a.runOnDevice(proj, external, listArgs...)
	if err != nil {
		return fmt.Errorf("%w\n\n  私有目录需要 debug 包（AndroidManifest 里 debuggable=true）\n  看外部目录可以加前缀: ak ls ext:\n", err)
	}
	rows := parseLsLong(out)
	if len(rows) == 0 {
		fmt.Println(out)
		return nil
	}
	printLs(abs, rows)
	return nil
}

func (a *App) cmdTree(args []string) error {
	fs := flag.NewFlagSet("tree", flag.ContinueOnError)
	depth := fs.Int("L", 2, "最大深度")
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.fileProject()
	if err != nil {
		return err
	}
	target := ""
	if rest := fs.Args(); len(rest) > 0 {
		target = rest[0]
	}
	abs, external := a.resolveRemote(proj, target)
	out, err := a.runOnDevice(proj, external, "find", abs, "-maxdepth", strconv.Itoa(*depth))
	if err != nil {
		return fmt.Errorf("%w\n\n  私有目录需要 debug 包；看外部目录可以加前缀: ak tree ext:", err)
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != abs {
			paths = append(paths, line)
		}
	}
	sort.Strings(paths)
	dirSet := map[string]bool{}
	for _, p := range paths {
		for q := path.Dir(p); q != "/" && q != abs && strings.HasPrefix(q, abs); q = path.Dir(q) {
			dirSet[q] = true
		}
	}
	fmt.Printf("%s  (%d 项，深度 %d)\n", abs, len(paths)+1, *depth)
	for _, p := range paths {
		rel := strings.TrimPrefix(strings.TrimPrefix(p, abs), "/")
		indent := strings.Repeat("  ", strings.Count(rel, "/")+1)
		name := path.Base(p)
		if dirSet[p] {
			name += "/"
		}
		fmt.Printf("%s%s\n", indent, name)
	}
	return nil
}

func (a *App) cmdCat(args []string) error {
	fs := flag.NewFlagSet("cat", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("用法: ak cat <文件>\n  例: ak cat shared_prefs/settings.xml")
	}
	proj, err := a.fileProject()
	if err != nil {
		return err
	}
	abs, external := a.resolveRemote(proj, rest[0])
	out, err := a.runOnDevice(proj, external, "cat", abs)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func (a *App) cmdSP(args []string) error {
	fs := flag.NewFlagSet("sp", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	proj, err := a.fileProject()
	if err != nil {
		return err
	}
	dir := "/data/data/" + proj.Package + "/shared_prefs"
	rest := fs.Args()
	if len(rest) == 0 {
		out, err := a.runOnDevice(proj, false, "ls", "-lt", dir)
		if err != nil {
			return fmt.Errorf("%w\n\n  私有目录需要 debug 包", err)
		}
		rows := parseLsLong(out)
		if len(rows) == 0 {
			fmt.Printf("%s 为空\n", dir)
			return nil
		}
		fmt.Printf("%s  (%d 个文件)\n\n", dir, len(rows))
		for _, r := range rows {
			name := strings.TrimSuffix(r.Name, ".xml")
			fmt.Printf("  %-28s %8s  %s\n", name, humanSize(r.Size), r.When)
		}
		fmt.Printf("\n查看内容: ak sp <名称>\n")
		return nil
	}
	name := rest[0]
	if !strings.HasSuffix(name, ".xml") {
		name += ".xml"
	}
	out, err := a.runOnDevice(proj, false, "cat", dir+"/"+name)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func (a *App) cmdDump(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	out := fs.String("o", "", "本地输出路径")
	outLong := fs.String("output", "", "本地输出路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("用法: ak dump <应用内路径> [-o 本地路径]\n  例: ak dump shared_prefs/settings.xml\n      ak dump files -o ~/Desktop/files")
	}
	proj, err := a.fileProject()
	if err != nil {
		return err
	}
	_, visible, err := a.Paths.EnsureProjectDirs(proj)
	if err != nil {
		return err
	}
	abs, external := a.resolveRemote(proj, rest[0])
	kind, err := a.remoteKind(proj, external, abs)
	if err != nil {
		return err
	}
	dest := strings.TrimSpace(*outLong)
	if dest == "" {
		dest = strings.TrimSpace(*out)
	}
	base := path.Base(abs)
	dumpRoot := filepath.Join(visible, "dump")
	if dest == "" {
		dest = filepath.Join(dumpRoot, base)
	} else if strings.HasSuffix(dest, "/") || strings.HasSuffix(dest, string(os.PathSeparator)) || isDir(dest) {
		dest = filepath.Join(dest, base)
	}
	if kind == "dir" {
		parent := filepath.Dir(dest)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		if external {
			if err := runStream("", nil, mustADB(), a.adbArgs(proj, "pull", abs, parent)...); err != nil {
				return err
			}
		} else {
			count, err := a.dumpDir(proj, abs, parent)
			if err != nil {
				return err
			}
			fmt.Printf("已 dump 目录 %s → %s（%d 个文件）\n", abs, dest, count)
			return nil
		}
		fmt.Printf("已 dump 目录 %s → %s\n", abs, dest)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	var data []byte
	if external {
		tmp := dest + ".ak-tmp"
		if err := runStream("", nil, mustADB(), a.adbArgs(proj, "pull", abs, tmp)...); err != nil {
			return err
		}
		defer os.Remove(tmp)
		data, err = os.ReadFile(tmp)
	} else {
		data, err = a.captureOut(proj, "run-as "+shellQuote(proj.Package)+" cat "+shellQuote(abs))
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("已 dump %s → %s（%s）\n", abs, dest, byteSize(int64(len(data))))
	return nil
}

func mustADB() string {
	adb, _ := lookPath("adb")
	return adb
}

func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// dumpDir 用 tar 流把设备上的目录整棵拉下来，避免逐文件 adb。
func (a *App) dumpDir(proj *Project, abs, destParent string) (int, error) {
	script := "run-as " + shellQuote(proj.Package) + " tar -cf - -C " +
		shellQuote(path.Dir(abs)) + " " + shellQuote(path.Base(abs))
	cmd := a.execOut(proj, script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	count, err := untarInto(stdout, destParent)
	if werr := cmd.Wait(); werr != nil {
		if err == nil {
			err = fmt.Errorf("%v: %s", werr, strings.TrimSpace(stderr.String()))
		}
	}
	return count, err
}

func untarInto(r io.Reader, destParent string) (int, error) {
	tr := tar.NewReader(r)
	count := 0
	root, _ := filepath.Abs(destParent)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." || strings.HasPrefix(name, "..") {
			continue
		}
		target := filepath.Join(root, name)
		if !strings.HasPrefix(target, root+string(os.PathSeparator)) && target != root {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return count, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return count, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return count, err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return count, err
			}
			f.Close()
			count++
		}
	}
	return count, nil
}

func (a *App) cmdPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("用法: ak push <本地文件> [目标路径]\n  例: ak push ./config.json files/config.json\n      ak push ./a.zip ext:download/a.zip")
	}
	proj, err := a.fileProject()
	if err != nil {
		return err
	}
	local := rest[0]
	if !exists(local) {
		return fmt.Errorf("本地文件不存在: %s", local)
	}
	remote := ""
	if len(rest) > 1 {
		remote = rest[1]
	}
	abs, external := a.resolveRemote(proj, remote)
	if remote == "" {
		abs = "/data/data/" + proj.Package + "/files/" + filepath.Base(local)
	}
	if strings.HasSuffix(remote, "/") {
		abs = cleanRemotePath(path.Join(abs, filepath.Base(local)))
	}
	if external {
		parent := path.Dir(abs)
		_, _ = a.runOnDevice(proj, true, "mkdir", "-p", parent)
		if err := runStream("", nil, mustADB(), a.adbArgs(proj, "push", local, abs)...); err != nil {
			return err
		}
		fmt.Printf("已推送 %s → %s\n", local, abs)
		return nil
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	script := "run-as " + shellQuote(proj.Package) + " sh -c " +
		shellQuote("cat > "+shellQuote(abs))
	adb, ok := lookPath("adb")
	if !ok {
		return fmt.Errorf("找不到 adb")
	}
	cmdArgs := []string{}
	if serial := a.device(proj); serial != "" {
		cmdArgs = append(cmdArgs, "-s", serial)
	}
	cmdArgs = append(cmdArgs, "shell", script)
	cmd := exec.Command(adb, cmdArgs...)
	cmd.Stdin = f
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\n\n  私有目录需要 debug 包（debuggable=true）", err)
	}
	fmt.Printf("已推送 %s → %s\n", local, abs)
	return nil
}
