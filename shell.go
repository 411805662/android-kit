package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func (a *App) cmdShell(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: ak shell init|refresh|stamp|install|uninstall")
	}
	sub := args[0]
	fs := flag.NewFlagSet("shell "+sub, flag.ContinueOnError)
	shell := fs.String("shell", "auto", "zsh|bash|auto")
	quiet := fs.Bool("quiet", false, "安静模式")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	_ = shell
	switch sub {
	case "init":
		fmt.Print(a.shellScript(true, *quiet))
	case "refresh":
		fmt.Print(a.shellScript(false, true))
	case "stamp":
		fmt.Println(Stamp(a.Paths))
	case "install":
		_, err := a.installShellBlock()
		return err
	case "uninstall":
		return a.removeShellBlock(false)
	default:
		return fmt.Errorf("未知子命令: ak shell %s", sub)
	}
	return nil
}

func (a *App) shellScript(full, quiet bool) string {
	var b strings.Builder
	bin := shellQuote(a.Paths.Binary)
	b.WriteString("# android-kit shell integration\n")
	fmt.Fprintf(&b, "_AK_BIN=%s\n", bin)
	b.WriteString("function ak { command \"$_AK_BIN\" \"$@\"; }\n")

	if a.Config.Aliases.Preset != "none" && len(a.Config.Aliases.Items) > 0 {
		names := make([]string, 0, len(a.Config.Aliases.Items))
		for name := range a.Config.Aliases.Items {
			names = append(names, name)
		}
		sort.Strings(names)
		b.WriteString("\nif [ \"${ANDROID_KIT_NO_ALIASES:-0}\" != \"1\" ]; then\n")
		for _, name := range names {
			value := strings.TrimSpace(a.Config.Aliases.Items[name])
			if value == "" || name == "ak" {
				continue
			}
			kind, body := aliasTarget(value)
			if name == "in" {
				fmt.Fprintf(&b, "  if [ \"${_AK_ALIAS_in:-}\" = \"1\" ] || [ %t = true ] || ! alias in >/dev/null 2>&1; then\n", a.Config.Aliases.Force)
				b.WriteString("    unalias in 2>/dev/null || true\n")
				if kind == "shell" {
					fmt.Fprintf(&b, "    alias in='%s'\n", strings.ReplaceAll(body, "'", `'\''`))
				} else {
					fmt.Fprintf(&b, "    alias in='ak %s'\n", strings.ReplaceAll(body, "'", `'\''`))
				}
				b.WriteString("    _AK_ALIAS_in=1\n")
				b.WriteString("  fi\n")
				continue
			}
			guard := fmt.Sprintf("[ \"${_AK_ALIAS_%s:-}\" = \"1\" ] || [ %t = true ] || ! type %s >/dev/null 2>&1", name, a.Config.Aliases.Force, name)
			fmt.Fprintf(&b, "  if %s; then\n", guard)
			fmt.Fprintf(&b, "    unalias %s 2>/dev/null || true\n", name)
			if kind == "shell" {
				fmt.Fprintf(&b, "    function %s { sh -c %s ak \"$@\"; }\n", name, shellQuote(body))
			} else if body == "cd" || strings.HasPrefix(body, "cd ") {
				// cd 切到当前项目；cd <项目> 切到指定项目，用于 dev / dev2 这类目录快捷
				target := ""
				if body != "cd" {
					target = " " + shellQuote(strings.TrimSpace(strings.TrimPrefix(body, "cd")))
				}
				fmt.Fprintf(&b, "    function %s { builtin cd \"$(command \"$_AK_BIN\" where%s 2>/dev/null)\" || return 1; }\n", name, target)
			} else {
				fmt.Fprintf(&b, "    function %s { command \"$_AK_BIN\" %s \"$@\"; }\n", name, quoteFields(body))
			}
			fmt.Fprintf(&b, "    _AK_ALIAS_%s=1\n", name)
			b.WriteString("  fi\n")
		}
		b.WriteString("fi\n")
	}

	if full {
		b.WriteString(`
_AK_STAMP="$(command "$_AK_BIN" shell stamp 2>/dev/null)"
function _ak_precmd {
  local _ak_s
  _ak_s="$(command "$_AK_BIN" shell stamp 2>/dev/null)" || return 0
  if [ "$_ak_s" != "$_AK_STAMP" ]; then
    _AK_STAMP="$_ak_s"
    eval "$(command "$_AK_BIN" shell refresh 2>/dev/null)"
  fi
}
if [ -n "${ZSH_VERSION:-}" ]; then
  autoload -Uz add-zsh-hook 2>/dev/null
  add-zsh-hook -d precmd _ak_precmd 2>/dev/null
  add-zsh-hook precmd _ak_precmd 2>/dev/null
elif [ -n "${BASH_VERSION:-}" ]; then
  case ";${PROMPT_COMMAND:-};" in
    *";_ak_precmd;"*) ;;
    *) PROMPT_COMMAND="_ak_precmd${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
  esac
fi
if [ -n "${ZSH_VERSION:-}" ] && command -v compdef >/dev/null 2>&1; then
  function _ak_complete {
    local -a _ak_cmds
    _ak_cmds=(web cd devices project use status doctor run build install stop open restart clear clear-sp screenshot layout text paths logcat tap swipe key proxy preview analyze docs alias clean uninstall unset)
    _describe 'ak command' _ak_cmds
  }
  compdef _ak_complete ak 2>/dev/null
elif [ -n "${BASH_VERSION:-}" ]; then
  complete -W "web cd devices project use status doctor run build install stop open restart clear clear-sp screenshot layout text paths logcat tap swipe key proxy preview analyze docs alias clean uninstall unset" ak 2>/dev/null
fi
`)
	}
	if !quiet {
		b.WriteString("# android-kit loaded\n")
	}
	return b.String()
}

func aliasTarget(value string) (kind, body string) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "shell:") {
		return "shell", strings.TrimSpace(strings.TrimPrefix(value, "shell:"))
	}
	if strings.HasPrefix(value, "ak:") {
		return "ak", strings.TrimSpace(strings.TrimPrefix(value, "ak:"))
	}
	return "ak", value
}

func quoteFields(value string) string {
	fields := strings.Fields(value)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, shellQuote(f))
	}
	return strings.Join(quoted, " ")
}

const (
	shellBlockStart = "# >>> android-kit >>>"
	shellBlockEnd   = "# <<< android-kit <<<"
)

func (a *App) shellBlock() string {
	return shellBlockStart + `
if command -v android-kit >/dev/null 2>&1; then
  eval "$(android-kit shell init --quiet 2>/dev/null)"
fi
` + shellBlockEnd + "\n"
}

func (a *App) rcFiles() []string {
	home, _ := os.UserHomeDir()
	var files []string
	for _, name := range []string{".zshrc", ".bash_profile", ".bashrc"} {
		path := home + string(os.PathSeparator) + name
		if exists(path) {
			files = append(files, path)
		}
	}
	return files
}

func (a *App) installShellBlock() (map[string]string, error) {
	block := a.shellBlock()
	changed := 0
	backups := map[string]string{}
	for _, file := range a.rcFiles() {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		text := string(data)
		if strings.Contains(text, shellBlockStart) {
			start := strings.Index(text, shellBlockStart)
			end := strings.Index(text, shellBlockEnd)
			if end > start {
				text = text[:start] + block + text[end+len(shellBlockEnd):]
			}
		} else {
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += "\n" + block
		}
		if text == string(data) {
			continue
		}
		backup := a.backupFile(file)
		if err := atomicWrite(file, []byte(text), 0o644); err != nil {
			return backups, err
		}
		backups[file] = backup
		fmt.Printf("updated %s (backup: %s)\n", file, backup)
		changed++
	}
	if changed == 0 {
		fmt.Println("shell integration already up to date")
	}
	return backups, nil
}

func (a *App) backupFile(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	name := timestamp() + "_" + strings.TrimPrefix(filepathBase(file), ".")
	path := filepathJoin(a.Paths.BackupDir, name)
	_ = atomicWrite(path, data, 0o600)
	return path
}

func (a *App) removeShellBlock(dryRun bool) error {
	removed := 0
	for _, file := range a.rcFiles() {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		text := string(data)
		start := strings.Index(text, shellBlockStart)
		if start < 0 {
			continue
		}
		end := strings.Index(text[start:], shellBlockEnd)
		if end < 0 {
			continue
		}
		end = start + end + len(shellBlockEnd)
		for end < len(text) && (text[end] == '\n' || text[end] == '\r') {
			end++
		}
		newText := text[:start] + text[end:]
		if dryRun {
			fmt.Printf("would remove android-kit block from %s\n", file)
			removed++
			continue
		}
		backup := a.backupFile(file)
		if err := atomicWrite(file, []byte(newText), 0o644); err != nil {
			return err
		}
		fmt.Printf("removed block from %s (backup: %s)\n", file, backup)
		removed++
	}
	if removed == 0 {
		fmt.Println("no android-kit shell block found")
	}
	return nil
}

func filepathBase(p string) string {
	parts := strings.Split(strings.ReplaceAll(p, "\\", "/"), "/")
	return parts[len(parts)-1]
}

func filepathJoin(parts ...string) string {
	return strings.Join(parts, string(os.PathSeparator))
}
