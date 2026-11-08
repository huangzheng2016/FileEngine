package executor

import (
	"context"
	"path"
	"sort"
	"strings"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

func (e *Executor) Validate(ctx context.Context, sessionID uint, opts Options) (*Result, error) {
	e.sessionID = sessionID
	res := &Result{}
	entries, err := e.plan(ctx, opts, res)
	if err != nil {
		return nil, err
	}
	res.Total = len(entries) + res.Skipped
	return res, nil
}

func (e *Executor) plan(ctx context.Context, opts Options, res *Result) ([]db.FileEntry, error) {
	all, err := e.repo.ListPlannedEntries(e.sessionID)
	if err != nil {
		return nil, err
	}
	decided, err := e.repo.ListDecidedEntries(e.sessionID)
	if err != nil {
		return nil, err
	}
	decidedPaths := make(map[string]bool, len(decided))
	for _, d := range decided {
		decidedPaths[cleanPath(d.OriginalPath)] = true
	}

	// Collisions are judged over the whole plan so a scoped run agrees with a
	// full one: the first entry in plan order keeps the target.
	firstByKey := map[string]uint{}
	for _, f := range all {
		k := db.CollisionKey(f.NewPath)
		if _, ok := firstByKey[k]; !ok {
			firstByKey[k] = f.ID
		}
	}

	scope := cleanPath(opts.CategoryPath)
	var ok []db.FileEntry
	for i := range all {
		f := &all[i]
		dst := cleanPath(f.NewPath)
		if opts.CategoryPath != "" && dst != scope && !strings.HasPrefix(dst, scope+"/") {
			continue
		}
		code, detail := staticIssue(f, decidedPaths, firstByKey)
		if code != "" {
			if opts.Sample <= 0 {
				res.Skipped++
				res.issue(f, code, detail)
			}
			continue
		}
		ok = append(ok, *f)
		if opts.Sample > 0 && len(ok) == opts.Sample {
			break
		}
	}

	ok = e.checkFS(ctx, ok, res)
	return ok, ctx.Err()
}

func staticIssue(f *db.FileEntry, decided map[string]bool, firstByKey map[string]uint) (string, string) {
	if err := validTarget(f.NewPath); err != nil {
		return IssueInvalidTarget, err.Error()
	}
	src, dst := cleanPath(f.OriginalPath), cleanPath(f.NewPath)
	sk, dk := db.CollisionKey(src), db.CollisionKey(dst)
	if sk == dk {
		return IssueIntoSelf, "目标与源相同"
	}
	if strings.HasPrefix(dk, sk+"/") {
		return IssueIntoSelf, "不能把目录移动到它自己的子目录中"
	}
	for p := parentOf(src); p != ""; p = parentOf(p) {
		if decided[p] {
			return IssueNested, "上级目录「" + p + "」已作为整体处理"
		}
	}
	if firstByKey[dk] != f.ID {
		return IssueCollision, "另一项也以此为目标（不区分大小写/Unicode 规范化）"
	}
	return "", ""
}

func (e *Executor) checkFS(ctx context.Context, entries []db.FileEntry, res *Result) []db.FileEntry {
	if len(entries) == 0 {
		return entries
	}
	dirSet := map[string]bool{}
	for _, f := range entries {
		dirSet[parentOf(cleanPath(f.OriginalPath))] = true
		dirSet[parentOf(cleanPath(f.NewPath))] = true
	}
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	listings := e.listConcurrent(ctx, dirs)

	byName := make(map[string]map[string]remotefs.FileInfo, len(listings))
	byKey := make(map[string]map[string]bool, len(listings))
	for d, list := range listings {
		names, keys := map[string]remotefs.FileInfo{}, map[string]bool{}
		for _, fi := range list {
			names[fi.Name] = fi
			keys[db.CollisionKey(fi.Name)] = true
		}
		byName[d], byKey[d] = names, keys
	}

	var out []db.FileEntry
	for i := range entries {
		f := &entries[i]
		src, dst := cleanPath(f.OriginalPath), cleanPath(f.NewPath)
		if names, listed := byName[parentOf(src)]; listed {
			st, found := names[path.Base(src)]
			if !found {
				res.Skipped++
				res.issue(f, IssueSourceMissing, "源文件不存在")
				continue
			}
			if reason := staleReason(f, &st); reason != "" {
				res.Skipped++
				res.issue(f, IssueSourceChanged, reason)
				continue
			}
		}
		if keys, listed := byKey[parentOf(dst)]; listed && keys[db.CollisionKey(path.Base(dst))] {
			if !(f.FileType == "directory" && e.owned(dst)) {
				res.Skipped++
				res.issue(f, IssueTargetExists, "目标位置已有同名文件或目录（不区分大小写）")
				continue
			}
		}
		out = append(out, *f)
	}
	return out
}
