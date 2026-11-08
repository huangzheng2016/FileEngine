package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"FileEngine/internal/db"
)

// Marker kinds.
const (
	markerCode     = "code"     // source project root
	markerBundle   = "bundle"   // OS bundle that must stay whole (.app, .photoslibrary)
	markerPhotos   = "photos"   // camera card dump
	markerVideo    = "video"    // DVD rip
	markerSoftware = "software" // installer / portable software folder
)

// Marker is one deterministic hint that a directory is a whole unit.
type Marker struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

var childMarkers = map[string]Marker{
	".git":             {Kind: markerCode, Label: "Git 仓库"},
	"go.mod":           {Kind: markerCode, Label: "Go 项目"},
	"package.json":     {Kind: markerCode, Label: "Node.js 项目"},
	"cargo.toml":       {Kind: markerCode, Label: "Rust 项目"},
	"pom.xml":          {Kind: markerCode, Label: "Java 项目"},
	"build.gradle":     {Kind: markerCode, Label: "Java/Android 项目"},
	"build.gradle.kts": {Kind: markerCode, Label: "Java/Android 项目"},
	"cmakelists.txt":   {Kind: markerCode, Label: "C/C++ 项目"},
	"pyproject.toml":   {Kind: markerCode, Label: "Python 项目"},
	"setup.py":         {Kind: markerCode, Label: "Python 项目"},
	"composer.json":    {Kind: markerCode, Label: "PHP 项目"},
	"gemfile":          {Kind: markerCode, Label: "Ruby 项目"},
	"dcim":             {Kind: markerPhotos, Label: "相机存储卡导出"},
	"video_ts":         {Kind: markerVideo, Label: "DVD 影片"},
}

var suffixMarkers = []struct {
	suffix string
	m      Marker
}{
	{".sln", Marker{Kind: markerCode, Label: ".NET 项目"}},
	{".csproj", Marker{Kind: markerCode, Label: ".NET 项目"}},
	{".xcodeproj", Marker{Kind: markerCode, Label: "Xcode 项目"}},
}

var selfMarkers = []struct {
	suffix string
	m      Marker
}{
	{".app", Marker{Kind: markerBundle, Label: "macOS 应用"}},
	{".photoslibrary", Marker{Kind: markerBundle, Label: "照片图库"}},
	{".bundle", Marker{Kind: markerBundle, Label: "应用包"}},
}

// ExtCount is one bucket of the extension histogram.
type ExtCount struct {
	Ext   string `json:"ext"`
	Count int    `json:"count"`
}

// DirSignals are the deterministic facts shown with a directory unit.
type DirSignals struct {
	Markers    []Marker       `json:"markers,omitempty"`
	Exts       []ExtCount     `json:"exts,omitempty"` // direct child files, most common first (≤8)
	Files      int            `json:"files"`          // direct child files
	Dirs       int            `json:"dirs"`           // direct child directories
	TotalFiles int64          `json:"total_files"`    // recursive
	TotalSize  int64          `json:"total_size"`
	Depth      int            `json:"depth"`
	Versions   []VersionGroup `json:"versions,omitempty"`
}

// ComputeSignals derives the signals of dir from its direct children and the
// recursive totals.
func ComputeSignals(dir *db.FileEntry, children []db.FileEntry, totalFiles, totalSize int64) DirSignals {
	s := DirSignals{TotalFiles: totalFiles, TotalSize: totalSize}
	if dir != nil {
		s.Depth = dir.Depth
		lower := strings.ToLower(dir.Name)
		for _, sm := range selfMarkers {
			if strings.HasSuffix(lower, sm.suffix) {
				m := sm.m
				m.Name = dir.Name
				s.Markers = append(s.Markers, m)
			}
		}
	}
	exts := map[string]int{}
	hasInstaller, hasDLL := "", false
	for _, c := range children {
		lower := strings.ToLower(c.Name)
		if m, ok := childMarkers[lower]; ok {
			m.Name = c.Name
			s.Markers = append(s.Markers, m)
		}
		for _, sm := range suffixMarkers {
			if strings.HasSuffix(lower, sm.suffix) {
				m := sm.m
				m.Name = c.Name
				s.Markers = append(s.Markers, m)
			}
		}
		if c.FileType == "directory" {
			s.Dirs++
			continue
		}
		s.Files++
		ext := db.FileExt(c.Name)
		if ext == "" {
			ext = "(无)"
		}
		exts[ext]++
		switch {
		case lower == "setup.exe" || lower == "install.exe" || ext == "msi":
			hasInstaller = c.Name
		case ext == "dll":
			hasDLL = true
		}
	}
	if hasInstaller != "" || (hasDLL && exts["exe"] > 0) {
		name := hasInstaller
		if name == "" {
			name = "*.exe + *.dll"
		}
		s.Markers = append(s.Markers, Marker{Name: name, Kind: markerSoftware, Label: "Windows 软件/安装程序"})
	}
	for e, n := range exts {
		s.Exts = append(s.Exts, ExtCount{Ext: e, Count: n})
	}
	sort.Slice(s.Exts, func(i, j int) bool {
		if s.Exts[i].Count != s.Exts[j].Count {
			return s.Exts[i].Count > s.Exts[j].Count
		}
		return s.Exts[i].Ext < s.Exts[j].Ext
	})
	if len(s.Exts) > 8 {
		s.Exts = s.Exts[:8]
	}
	s.Versions = DetectVersionGroups(children)
	return s
}

// strongMarker returns the first marker that by itself identifies a whole
// unit (project root or OS bundle), or nil.
func (s DirSignals) strongMarker() *Marker {
	for i := range s.Markers {
		if k := s.Markers[i].Kind; k == markerCode || k == markerBundle {
			return &s.Markers[i]
		}
	}
	return nil
}

// Render formats the signals for a unit prompt.
func (s DirSignals) Render() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("- 规模：直接子项 %d 个文件、%d 个目录；共 %d 个文件，%s；深度 %d\n", s.Files, s.Dirs, s.TotalFiles, humanBytes(s.TotalSize), s.Depth))
	if len(s.Markers) > 0 {
		parts := make([]string, len(s.Markers))
		for i, m := range s.Markers {
			parts[i] = fmt.Sprintf("%s（%s）", m.Name, m.Label)
		}
		sb.WriteString("- 标记：" + strings.Join(parts, "、") + "\n")
	}
	if len(s.Exts) > 0 {
		parts := make([]string, len(s.Exts))
		for i, e := range s.Exts {
			parts[i] = fmt.Sprintf("%s %d", e.Ext, e.Count)
		}
		sb.WriteString("- 扩展名：" + strings.Join(parts, "，") + "\n")
	}
	for _, v := range s.Versions {
		sb.WriteString("- 版本组：" + v.Render() + "\n")
	}
	return sb.String()
}

// VersionMember is one entry of a version group.
type VersionMember struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// VersionGroup is a set of siblings that are versions of the same thing
// ("app-1.2.zip", "app-1.3.zip"); it is decided as one work unit.
type VersionGroup struct {
	Name    string          `json:"name"`
	Members []VersionMember `json:"members"`
}

func (v VersionGroup) Render() string {
	parts := make([]string, len(v.Members))
	for i, m := range v.Members {
		parts[i] = m.Name
	}
	return fmt.Sprintf("「%s」%d 个版本：%s", v.Name, len(v.Members), strings.Join(parts, "、"))
}

var (
	versionRe   = regexp.MustCompile(`(?i)(^|[ _.\-])v?\d+(?:[._]\d+)+[a-z0-9]*|(^|[ _.\-])v\d+\b|(^|[ _.\-(\[])(19|20)\d\d(?:[)\]]|\b)`)
	noiseRe     = regexp.MustCompile(`(?i)(^|[ _.\-])(x64|x86|amd64|arm64|aarch64|win64|win32|setup|installer|portable|full|final|release|latest)\b|\s*\(\d+\)`)
	trimSepRe   = regexp.MustCompile(`[ _.\-()\[\]]+`)
	versionExts = map[string]bool{
		"exe": true, "msi": true, "dmg": true, "pkg": true, "zip": true, "7z": true, "rar": true, "iso": true,
		"apk": true, "ipa": true, "deb": true, "rpm": true, "appimage": true, "tar.gz": true, "tgz": true, "jar": true,
	}
)

// versionKey splits a name into its normalized base name and version token;
// ok is false when the name carries no version.
func versionKey(e db.FileEntry) (key, display, version string, ok bool) {
	stem := e.Name
	if e.FileType != "directory" {
		ext := db.FileExt(e.Name)
		if !versionExts[ext] {
			return "", "", "", false
		}
		stem = e.Name[:len(e.Name)-len(ext)-1]
	}
	loc := versionRe.FindStringIndex(stem)
	if loc == nil {
		return "", "", "", false
	}
	version = strings.Trim(stem[loc[0]:loc[1]], " _.-()[]")
	base := noiseRe.ReplaceAllString(versionRe.ReplaceAllString(stem, " "), " ")
	display = strings.Trim(trimSepRe.ReplaceAllString(base, " "), " ")
	if display == "" || version == "" {
		return "", "", "", false
	}
	return strings.ToLower(display), display, version, true
}

// DetectVersionGroups groups undecided siblings whose names differ only by a
// version (and arch / setup noise). Only directories and software/archive
// files qualify, so numbered photos never form a group.
func DetectVersionGroups(entries []db.FileEntry) []VersionGroup {
	type acc struct {
		display string
		members []VersionMember
	}
	groups := map[string]*acc{}
	var keys []string
	for _, e := range entries {
		if e.Role != db.RoleUndecided || e.FileType == "symlink" {
			continue
		}
		key, display, version, ok := versionKey(e)
		if !ok {
			continue
		}
		g := groups[key]
		if g == nil {
			g = &acc{display: display}
			groups[key] = g
			keys = append(keys, key)
		}
		g.members = append(g.members, VersionMember{Path: e.OriginalPath, Name: e.Name, Version: version})
	}
	sort.Strings(keys)
	var out []VersionGroup
	for _, k := range keys {
		g := groups[k]
		if len(g.members) < 2 {
			continue
		}
		sort.Slice(g.members, func(i, j int) bool { return g.members[i].Name < g.members[j].Name })
		out = append(out, VersionGroup{Name: g.display, Members: g.members})
	}
	return out
}
