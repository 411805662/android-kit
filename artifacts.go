package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ArtifactItem struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	URL       string `json:"url"`
	ViewerURL string `json:"viewer_url,omitempty"`
	Size      int64  `json:"size"`
	ModTime   string `json:"mod_time"`
	ProjectID string `json:"project_id"`
}

func (a *App) artifactDirs(proj *Project) (hidden, visible string) {
	if proj == nil {
		return "", ""
	}
	hidden = a.Paths.Artifacts
	visible = filepath.Join(proj.Root, "android-kit")
	return hidden, visible
}

func (a *App) webProject(projectID string) (*Project, error) {
	if projectID != "" {
		if proj := a.Store.Get(projectID); proj != nil {
			return proj, nil
		}
	}
	return a.project(false)
}

func (a *App) projectArtifacts(projectID string) []ArtifactItem {
	proj, err := a.webProject(projectID)
	if err != nil || proj == nil {
		return nil
	}
	hidden, visible := a.artifactDirs(proj)
	shotDir := filepath.Join(visible, "screenshots")
	uiDir := filepath.Join(hidden, "ui")
	shots := listArtifactFiles(shotDir, "screenshot", func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp"
	})
	uis := listArtifactFiles(uiDir, "ui", func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return ext == ".json" || ext == ".xml"
	})
	items := append(shots, uis...)
	for i := range items {
		items[i].URL = "/artifact?path=" + url.QueryEscape(items[i].Path)
		items[i].ProjectID = proj.ID
	}
	for i := range items {
		if items[i].Kind != "ui" || strings.ToLower(filepath.Ext(items[i].Name)) != ".json" {
			continue
		}
		prefix := strings.TrimSuffix(items[i].Name, filepath.Ext(items[i].Name))
		bestIndex := -1
		fallbackIndex := -1
		for shotIndex := range shots {
			shot := shots[shotIndex]
			shotPrefix := strings.TrimSuffix(shot.Name, filepath.Ext(shot.Name))
			if fallbackIndex == -1 || shot.ModTime > shots[fallbackIndex].ModTime {
				fallbackIndex = shotIndex
			}
			if shot.ModTime <= items[i].ModTime && (bestIndex == -1 || shot.ModTime > shots[bestIndex].ModTime) {
				bestIndex = shotIndex
			}
			if shotPrefix == prefix {
				bestIndex = shotIndex
				break
			}
		}
		if bestIndex == -1 {
			bestIndex = fallbackIndex
		}
		viewer := "/viewer?ui=" + url.QueryEscape(items[i].Path) + "&project=" + url.QueryEscape(proj.ID)
		if bestIndex >= 0 {
			viewer += "&shot=" + url.QueryEscape(shots[bestIndex].Path)
		}
		items[i].ViewerURL = viewer
	}
	return items
}

func listArtifactFiles(dir, kind string, accept func(string) bool) []ArtifactItem {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	items := []ArtifactItem{}
	for _, entry := range entries {
		if entry.IsDir() || !accept(entry.Name()) {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, ArtifactItem{
			Kind:    kind,
			Name:    entry.Name(),
			Path:    full,
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModTime > items[j].ModTime })
	return items
}

func (a *App) allowedArtifactRoots() []string {
	roots := []string{a.Paths.Root}
	for _, proj := range a.Store.List() {
		_, visible := a.artifactDirs(proj)
		roots = append(roots,
			filepath.Join(visible, "screenshots"),
			filepath.Join(visible, "previews"),
			filepath.Join(visible, "reports"),
		)
	}
	return roots
}

func (a *App) resolveArtifactPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("缺少 path")
	}
	path := filepath.Clean(expandHome(raw))
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, _ := filepath.EvalSymlinks(abs)
	if resolved == "" {
		resolved = abs
	}
	for _, root := range a.allowedArtifactRoots() {
		rootAbs, _ := filepath.Abs(root)
		rootResolved, _ := filepath.EvalSymlinks(rootAbs)
		if rootResolved == "" {
			rootResolved = rootAbs
		}
		if resolved == rootResolved || strings.HasPrefix(resolved, rootResolved+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("路径不在 android-kit 产物目录内")
}

func (a *App) serveArtifact(w http.ResponseWriter, r *http.Request) {
	path, err := a.resolveArtifactPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("pretty") != "" && strings.EqualFold(filepath.Ext(path), ".json") {
		data, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var buf bytes.Buffer
		if json.Indent(&buf, data, "", "  ") != nil {
			buf.Reset()
			buf.Write(data)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>%s</title><style>
:root{color-scheme:dark}
body{margin:0;background:#0b0d12;color:#e8ecf4;font:12.5px/1.55 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
.bar{position:sticky;top:0;display:flex;gap:10px;align-items:center;padding:10px 16px;
  background:#10141c;border-bottom:1px solid #252b38;font-family:-apple-system,BlinkMacSystemFont,"PingFang SC",sans-serif}
.bar b{font-size:12.5px;font-weight:600}.bar span{color:#8b94a7;font-size:11.5px;word-break:break-all}
.bar .spacer{flex:1}
.bar button{background:#1a1f2b;border:1px solid #252b38;color:#e8ecf4;border-radius:7px;padding:5px 10px;cursor:pointer;font-size:11.5px}
pre{margin:0;padding:16px 20px;white-space:pre;tab-size:2}
</style></head><body>
<div class="bar"><b>JSON</b><span>%s</span><div class="spacer"></div>
<button onclick="navigator.clipboard.writeText(document.querySelector('pre').textContent)">复制</button></div>
<pre>%s</pre></body></html>`, html.EscapeString(filepath.Base(path)), html.EscapeString(path), html.EscapeString(buf.String()))
		return
	}
	http.ServeFile(w, r, path)
}

func (a *App) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE required", 405)
		return
	}
	path, err := a.resolveArtifactPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if err := os.Remove(path); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeHTTPJSON(w, map[string]any{"ok": true, "path": path})
}

func (a *App) clearArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var body struct {
		Project string `json:"project"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	proj, err := a.webProject(body.Project)
	if err != nil || proj == nil {
		http.Error(w, "没有可用项目", 400)
		return
	}
	hidden, visible := a.artifactDirs(proj)
	removed := 0
	for _, dir := range []string{filepath.Join(visible, "screenshots"), filepath.Join(hidden, "ui")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if err := os.Remove(filepath.Join(dir, entry.Name())); err == nil {
				removed++
			}
		}
	}
	writeHTTPJSON(w, map[string]any{"ok": true, "removed": removed})
}

type DisplayInfo struct {
	DPI    int     `json:"dpi"`
	Scale  float64 `json:"scale"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Source string  `json:"source"`
}

func (a *App) displayInfo(projectID string) DisplayInfo {
	info := DisplayInfo{DPI: 160, Scale: 1, Source: "默认 160 dpi（px = dp）"}
	proj, err := a.webProject(projectID)
	if err != nil || proj == nil {
		return info
	}
	densityOut, err := a.adbCapture(proj, "shell", "wm", "density")
	if err == nil {
		match := regexp.MustCompile(`\d+`).FindString(densityOut)
		if dpi, convErr := strconv.Atoi(match); convErr == nil && dpi > 0 {
			info.DPI = dpi
			info.Scale = float64(dpi) / 160
			info.Source = "adb shell wm density"
		}
	}
	sizeOut, err := a.adbCapture(proj, "shell", "wm", "size")
	if err == nil {
		match := regexp.MustCompile(`(\d+)x(\d+)`).FindStringSubmatch(sizeOut)
		if len(match) == 3 {
			info.Width, _ = strconv.Atoi(match[1])
			info.Height, _ = strconv.Atoi(match[2])
		}
	}
	return info
}
