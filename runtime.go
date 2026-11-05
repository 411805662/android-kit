package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Device struct {
	Serial  string `json:"serial"`
	State   string `json:"state"`
	Model   string `json:"model"`
	Product string `json:"product"`
}

type Runtime struct {
	AndroidCLI        string `json:"android_cli"`
	AndroidCLIversion string `json:"android_cli_version"`
	ADB               string `json:"adb"`
	Java              string `json:"java"`
	GradleWrapper     bool   `json:"gradle_wrapper"`
}

func (p Paths) AndroidCLIPath(cfg Config) string {
	if cfg.AndroidCLI.Path != "" {
		return cfg.AndroidCLI.Path
	}
	if pth, ok := lookPath("android"); ok {
		return pth
	}
	return ""
}

func detectRuntime(p Paths, cfg Config) Runtime {
	rt := Runtime{}
	rt.AndroidCLI = p.AndroidCLIPath(cfg)
	if rt.AndroidCLI != "" {
		out, _ := runCapture("", nil, rt.AndroidCLI, "-V")
		rt.AndroidCLIversion = strings.TrimSpace(out)
		if rt.AndroidCLIversion == "" {
			out, _ = runCapture("", nil, rt.AndroidCLI, "--version")
			rt.AndroidCLIversion = strings.TrimSpace(out)
		}
	}
	rt.ADB, _ = lookPath("adb")
	rt.Java, _ = lookPath("java")
	rt.GradleWrapper = false
	return rt
}

func (r Runtime) HasAndroidCLI() bool { return r.AndroidCLI != "" }

func versionParts(v string) []int {
	var parts []int
	for _, seg := range strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == ' ' || r == '\n' }) {
		n, err := strconv.Atoi(seg)
		if err != nil {
			break
		}
		parts = append(parts, n)
	}
	return parts
}

func versionAtLeast(got, want string) bool {
	g, w := versionParts(got), versionParts(want)
	for i := 0; i < len(w); i++ {
		gv := 0
		if i < len(g) {
			gv = g[i]
		}
		if gv > w[i] {
			return true
		}
		if gv < w[i] {
			return false
		}
	}
	return true
}

func (p Paths) Devices() []Device {
	adb, ok := lookPath("adb")
	if !ok {
		return nil
	}
	out, err := exec.Command(adb, "devices", "-l").Output()
	if err != nil {
		return nil
	}
	var devices []Device
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := Device{Serial: fields[0], State: fields[1]}
		for _, f := range fields[2:] {
			if strings.HasPrefix(f, "model:") {
				d.Model = strings.ReplaceAll(strings.TrimPrefix(f, "model:"), "_", " ")
			}
			if strings.HasPrefix(f, "product:") {
				d.Product = strings.TrimPrefix(f, "product:")
			}
		}
		devices = append(devices, d)
	}
	return devices
}

func (p Paths) PickDevice(proj *Project, override string) string {
	if override != "" {
		return override
	}
	if proj != nil && proj.Device != "" {
		return proj.Device
	}
	cfg := LoadConfig(p)
	if cfg.DefaultProject != "" {
		_ = cfg
	}
	if serial := os.Getenv("ANDROID_SERIAL"); serial != "" {
		return serial
	}
	for _, d := range p.Devices() {
		if d.State == "device" {
			return d.Serial
		}
	}
	return ""
}

func (p Paths) GradleWrapper(proj *Project) string {
	if proj == nil {
		return ""
	}
	for _, name := range []string{"gradlew", "gradlew.bat"} {
		path := filepath.Join(proj.Root, name)
		if exists(path) {
			return path
		}
	}
	return ""
}

func (p Paths) EnsureProjectDirs(proj *Project) (hidden, visible string, err error) {
	if proj == nil {
		return "", "", errNoProject
	}
	hidden = p.Artifacts
	visible = filepath.Join(proj.Root, "android-kit")
	for _, dir := range []string{
		hidden, visible,
		filepath.Join(hidden, "ui"),
		filepath.Join(hidden, "traces"),
		filepath.Join(hidden, "heaps"),
		filepath.Join(hidden, "reports"),
		filepath.Join(hidden, "raw"),
		filepath.Join(hidden, "logs"),
		filepath.Join(hidden, "tmp"),
		filepath.Join(visible, "screenshots"),
		filepath.Join(visible, "previews"),
		filepath.Join(visible, "reports"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
	}
	ignore := filepath.Join(visible, ".gitignore")
	if !exists(ignore) {
		_ = os.WriteFile(ignore, []byte("*\n"), 0o644)
	}
	return hidden, visible, nil
}

func (d Device) Label() string {
	if d.Model != "" {
		return fmt.Sprintf("%s (%s, %s)", d.Serial, d.Model, d.State)
	}
	return fmt.Sprintf("%s (%s)", d.Serial, d.State)
}
