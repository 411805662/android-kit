package main

import (
	"encoding/json"
	"fmt"
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
	ShotURL   string `json:"shot_url,omitempty"`
	Size      int64  `json:"size"`
	ModTime   string `json:"mod_time"`
	ProjectID string `json:"project_id"`
	Activity  string `json:"activity,omitempty"`
}

func (a *App) artifactDirs(proj *Project) (hidden, visible string) {
	if proj == nil {
		return "", ""
	}
	hidden = a.Paths.Artifacts
	visible = filepath.Join(proj.Root, "android-kit")
	return hidden, visible
}

// uiDir 是某个项目的 UI 树目录：~/.android-kit/artifacts/<项目>/ui，
// 按项目分目录存放，网页上才能分项目查看。
func (a *App) uiDir(proj *Project) string {
	if proj == nil {
		return ""
	}
	return filepath.Join(a.Paths.Artifacts, proj.ID, "ui")
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
	_, visible := a.artifactDirs(proj)
	shotDir := filepath.Join(visible, "screenshots")
	uiDir := a.uiDir(proj)
	shots := listArtifactFiles(shotDir, "screenshot", func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp"
	})
	uis := listArtifactFiles(uiDir, "ui", func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return ext == ".json" || ext == ".xml"
	})
	items := make([]ArtifactItem, 0, len(shots)+len(uis))
	items = append(items, shots...)
	items = append(items, uis...)
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
			items[i].ShotURL = "/artifact?path=" + url.QueryEscape(shots[bestIndex].Path)
		}
		items[i].ViewerURL = viewer
		sidecar := strings.TrimSuffix(items[i].Path, filepath.Ext(items[i].Path)) + ".activity"
		if data, err := os.ReadFile(sidecar); err == nil {
			items[i].Activity = strings.TrimSpace(string(data))
		}
	}
	return items
}

// ArtifactGroup 是网页上的一组产物，一个项目一组。
type ArtifactGroup struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Root      string         `json:"root"`
	Current   bool           `json:"current"`
	Legacy    bool           `json:"legacy,omitempty"`
	Artifacts []ArtifactItem `json:"artifacts"`
}

// allArtifacts 按项目分组返回所有产物，当前项目排最前。
func (a *App) allArtifacts() []ArtifactGroup {
	current, _ := a.project(false)
	groups := make([]ArtifactGroup, 0, len(a.Store.List())+1)
	for _, p := range a.Store.List() {
		if p == nil || p.Root == "" {
			continue
		}
		groups = append(groups, ArtifactGroup{
			ID:        p.ID,
			Name:      p.Name,
			Root:      p.Root,
			Current:   current != nil && current.ID == p.ID,
			Artifacts: a.projectArtifacts(p.ID),
		})
	}
	// 早期版本把 UI 树存在共享目录里，这里兜底展示，避免历史产物直接消失
	if legacy := a.legacyArtifacts(); len(legacy) > 0 {
		groups = append(groups, ArtifactGroup{
			ID: "_misc", Name: "未归属（历史）", Legacy: true, Artifacts: legacy,
		})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Current != groups[j].Current {
			return groups[i].Current
		}
		if groups[i].Legacy != groups[j].Legacy {
			return !groups[i].Legacy
		}
		return groups[i].Name < groups[j].Name
	})
	return groups
}

func (a *App) legacyArtifacts() []ArtifactItem {
	dir := filepath.Join(a.Paths.Artifacts, "ui")
	items := listArtifactFiles(dir, "ui", func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return ext == ".json" || ext == ".xml"
	})
	for i := range items {
		items[i].URL = "/artifact?path=" + url.QueryEscape(items[i].Path)
		items[i].ProjectID = "_misc"
		if strings.ToLower(filepath.Ext(items[i].Name)) != ".json" {
			continue
		}
		items[i].ViewerURL = "/viewer?ui=" + url.QueryEscape(items[i].Path)
		sidecar := strings.TrimSuffix(items[i].Path, filepath.Ext(items[i].Path)) + ".activity"
		if data, err := os.ReadFile(sidecar); err == nil {
			items[i].Activity = strings.TrimSpace(string(data))
		}
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
		data, err := a.webPage("json.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
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
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".xml":
		_ = os.Remove(strings.TrimSuffix(path, filepath.Ext(path)) + ".activity")
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
	dirs := []string{filepath.Join(a.Paths.Artifacts, "ui")}
	for _, proj := range a.Store.List() {
		if proj == nil || proj.Root == "" {
			continue
		}
		_, visible := a.artifactDirs(proj)
		dirs = append(dirs, filepath.Join(visible, "screenshots"), a.uiDir(proj))
	}
	removed := 0
	for _, dir := range dirs {
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
