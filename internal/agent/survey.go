package agent

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

const (
	overviewTopDirsMax   = 40
	overviewSamplesPer   = 5
	overviewExtsMax      = 25
	overviewExtsPerDir   = 3
	overviewMarkersMax   = 30
	overviewRootFilesMax = 10
)

var overviewMarkerNames = map[string]string{
	".git": "Git 仓库", "package.json": "Node 项目", "go.mod": "Go 项目", "cargo.toml": "Rust 项目",
	"pom.xml": "Maven 项目", "build.gradle": "Gradle 项目", "cmakelists.txt": "CMake 项目",
	"requirements.txt": "Python 项目", "pyproject.toml": "Python 项目", "makefile": "Makefile 项目",
	"dcim": "相机目录", "video_ts": "DVD 光盘", "bdmv": "蓝光光盘", "setup.exe": "安装程序",
	"autorun.inf": "光盘镜像", ".svn": "SVN 仓库",
}

var overviewMarkerExts = map[string]string{
	".xcodeproj": "Xcode 项目", ".sln": "VS 解决方案", ".photoslibrary": "照片图库", ".app": "macOS 应用",
	".msi": "安装包",
}

type overviewDir struct {
	name    string
	files   int
	dirs    int
	size    int64
	exts    map[string]int
	samples []string
}

type overviewExt struct {
	ext   string
	count int
	size  int64
}

type overviewMarker struct {
	dir   string
	label string
}

type TreeOverview struct {
	Files, Dirs   int
	Size          int64
	MaxDepth      int
	DepthFiles    map[int]int // files per depth below the scan root (1 = directly in it)
	TopDirs       []overviewDir
	MoreTopDirs   int
	RootFiles     []string
	RootFileCount int
	Exts          []overviewExt
	MoreExts      int
	Markers       []overviewMarker
	MoreMarkers   int
}

func relToRoot(root, p string) string {
	root = strings.Trim(root, "/")
	p = strings.Trim(p, "/")
	if root == "" || root == "." {
		return p
	}
	if p == root {
		return ""
	}
	return strings.TrimPrefix(p, root+"/")
}

func fileExt(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" || ext == strings.ToLower(name) {
		return "(无扩展名)"
	}
	return ext
}

func ComputeTreeOverview(repo *db.Repository, session *db.ScanSession) (*TreeOverview, error) {
	ov := &TreeOverview{DepthFiles: map[int]int{}}
	top := map[string]*overviewDir{}
	exts := map[string]*overviewExt{}
	markerSeen := map[string]bool{}
	var markers []overviewMarker
	err := repo.EachEntry(session.ID, false, func(e db.EntryBrief) bool {
		rel := relToRoot(session.RootPath, e.OriginalPath)
		if rel == "" {
			return true
		}
		segs := strings.Split(rel, "/")
		lower := strings.ToLower(e.Name)
		label, isMarker := overviewMarkerNames[lower]
		dir := path.Dir(rel)
		if !isMarker {
			ext := strings.ToLower(path.Ext(e.Name))
			if label, isMarker = overviewMarkerExts[ext]; isMarker && e.FileType == "directory" && (ext == ".photoslibrary" || ext == ".app") {
				dir = rel // bundles mark themselves
			}
		}
		if isMarker {
			if key := dir + "|" + label; !markerSeen[key] {
				markerSeen[key] = true
				markers = append(markers, overviewMarker{dir: dir, label: label})
			}
		}
		if e.FileType == "directory" {
			ov.Dirs++
			if len(segs) == 1 {
				if top[segs[0]] == nil {
					top[segs[0]] = &overviewDir{name: segs[0], exts: map[string]int{}}
				}
			} else {
				d := top[segs[0]]
				if d == nil {
					d = &overviewDir{name: segs[0], exts: map[string]int{}}
					top[segs[0]] = d
				}
				d.dirs++
			}
			return true
		}
		ov.Files++
		ov.Size += e.Size
		depth := len(segs)
		ov.DepthFiles[depth]++
		if depth > ov.MaxDepth {
			ov.MaxDepth = depth
		}
		ext := fileExt(e.Name)
		x := exts[ext]
		if x == nil {
			x = &overviewExt{ext: ext}
			exts[ext] = x
		}
		x.count++
		x.size += e.Size
		if len(segs) == 1 {
			ov.RootFileCount++
			if len(ov.RootFiles) < overviewRootFilesMax {
				ov.RootFiles = append(ov.RootFiles, e.Name)
			}
			return true
		}
		d := top[segs[0]]
		if d == nil {
			d = &overviewDir{name: segs[0], exts: map[string]int{}}
			top[segs[0]] = d
		}
		d.files++
		d.size += e.Size
		d.exts[ext]++
		if len(d.samples) < overviewSamplesPer {
			d.samples = append(d.samples, strings.Join(segs[1:], "/"))
		}
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("tree overview: %w", err)
	}

	for _, d := range top {
		ov.TopDirs = append(ov.TopDirs, *d)
	}
	sort.Slice(ov.TopDirs, func(i, j int) bool {
		a, b := ov.TopDirs[i], ov.TopDirs[j]
		if a.files != b.files {
			return a.files > b.files
		}
		return a.name < b.name
	})
	if len(ov.TopDirs) > overviewTopDirsMax {
		ov.MoreTopDirs = len(ov.TopDirs) - overviewTopDirsMax
		ov.TopDirs = ov.TopDirs[:overviewTopDirsMax]
	}
	for _, x := range exts {
		ov.Exts = append(ov.Exts, *x)
	}
	sort.Slice(ov.Exts, func(i, j int) bool {
		if ov.Exts[i].count != ov.Exts[j].count {
			return ov.Exts[i].count > ov.Exts[j].count
		}
		return ov.Exts[i].ext < ov.Exts[j].ext
	})
	if len(ov.Exts) > overviewExtsMax {
		ov.MoreExts = len(ov.Exts) - overviewExtsMax
		ov.Exts = ov.Exts[:overviewExtsMax]
	}
	markers = outermostMarkers(markers)
	if len(markers) > overviewMarkersMax {
		ov.MoreMarkers = len(markers) - overviewMarkersMax
		markers = markers[:overviewMarkersMax]
	}
	ov.Markers = markers
	return ov, nil
}

// A project's vendored packages are not projects of their own.
func outermostMarkers(markers []overviewMarker) []overviewMarker {
	sort.Slice(markers, func(i, j int) bool {
		di, dj := strings.Count(markers[i].dir, "/"), strings.Count(markers[j].dir, "/")
		if di != dj {
			return di < dj
		}
		if markers[i].dir != markers[j].dir {
			return markers[i].dir < markers[j].dir
		}
		return markers[i].label < markers[j].label
	})
	var out []overviewMarker
	marked := map[string]bool{}
	for _, m := range markers {
		inside := false
		for _, a := range sourceAncestors(m.dir) {
			if marked[a] {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		if m.dir != "." {
			marked[m.dir] = true
		}
		out = append(out, m)
	}
	for i := range out {
		if out[i].dir == "." {
			out[i].dir = "（扫描根目录）"
		}
	}
	return out
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func topExts(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	if len(list) > n {
		list = list[:n]
	}
	parts := make([]string, len(list))
	for i, e := range list {
		parts[i] = fmt.Sprintf("%s×%d", e.k, e.v)
	}
	return strings.Join(parts, " ")
}

func (ov *TreeOverview) Render() string {
	var sb strings.Builder
	sb.WriteString("## 目录树概览（系统统计）\n\n")
	sb.WriteString(fmt.Sprintf("共 %d 个文件、%d 个目录，总大小 %s，最深 %d 层。\n\n", ov.Files, ov.Dirs, humanSize(ov.Size), ov.MaxDepth))

	if len(ov.DepthFiles) > 0 {
		depths := make([]int, 0, len(ov.DepthFiles))
		for d := range ov.DepthFiles {
			depths = append(depths, d)
		}
		sort.Ints(depths)
		parts := make([]string, len(depths))
		for i, d := range depths {
			parts[i] = fmt.Sprintf("%d 层 %d", d, ov.DepthFiles[d])
		}
		sb.WriteString("文件深度分布：" + strings.Join(parts, "，") + "\n\n")
	}

	if len(ov.TopDirs) > 0 {
		sb.WriteString("### 顶层目录（按文件数）\n\n")
		for _, d := range ov.TopDirs {
			sb.WriteString(fmt.Sprintf("- %s/ — %d 个文件，%d 个子目录，%s", d.name, d.files, d.dirs, humanSize(d.size)))
			if e := topExts(d.exts, overviewExtsPerDir); e != "" {
				sb.WriteString("；主要类型 " + e)
			}
			sb.WriteString("\n")
			if len(d.samples) > 0 {
				sb.WriteString("  示例：" + strings.Join(d.samples, "、") + "\n")
			}
		}
		if ov.MoreTopDirs > 0 {
			sb.WriteString(fmt.Sprintf("- …另有 %d 个顶层目录\n", ov.MoreTopDirs))
		}
		sb.WriteString("\n")
	}

	if ov.RootFileCount > 0 {
		sb.WriteString(fmt.Sprintf("### 根目录散文件（%d 个）\n\n", ov.RootFileCount))
		sb.WriteString(strings.Join(ov.RootFiles, "、"))
		if ov.RootFileCount > len(ov.RootFiles) {
			sb.WriteString(" …")
		}
		sb.WriteString("\n\n")
	}

	if len(ov.Exts) > 0 {
		sb.WriteString("### 扩展名分布\n\n")
		for _, x := range ov.Exts {
			sb.WriteString(fmt.Sprintf("- %s：%d 个，%s\n", x.ext, x.count, humanSize(x.size)))
		}
		if ov.MoreExts > 0 {
			sb.WriteString(fmt.Sprintf("- …另有 %d 种扩展名\n", ov.MoreExts))
		}
		sb.WriteString("\n")
	}

	if len(ov.Markers) > 0 {
		sb.WriteString("### 特征文件（可能需要整体保留的目录）\n\n")
		for _, m := range ov.Markers {
			sb.WriteString(fmt.Sprintf("- %s：%s\n", m.dir, m.label))
		}
		if ov.MoreMarkers > 0 {
			sb.WriteString(fmt.Sprintf("- …另有 %d 处\n", ov.MoreMarkers))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

var errNoPlanWritten = errors.New("助手没有写入整理方案")

func (a *Agent) RunSurvey(ctx context.Context, logger *Logger, instructions string) (string, error) {
	before, err := a.repo.GetPlan(a.sessionID)
	if err != nil {
		return "", err
	}
	ov, err := ComputeTreeOverview(a.repo, a.session)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(ov.Render())
	if s := strings.TrimSpace(instructions); s != "" {
		sb.WriteString("## 用户补充要求\n\n" + s + "\n\n")
	}
	sb.WriteString("请按系统提示的步骤调研这棵目录树，建立分类体系，并用 write_plan 写出整理方案。")

	res, err := a.runMainAgent(ctx, logger, mainRunSpec{
		label:       "survey",
		maxStep:     config.Get().Agent.MaxStep,
		instruction: surveyPrompt,
		messages:    []*schema.Message{{Role: schema.User, Content: sb.String()}},
	})
	if err != nil {
		return "", err
	}
	after, err := a.repo.GetPlan(a.sessionID)
	if err != nil {
		return "", err
	}
	if after.Version == before.Version {
		return res, errNoPlanWritten
	}
	return res, nil
}
