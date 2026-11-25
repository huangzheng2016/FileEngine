package api

import (
	"FileEngine/biz/dal/db/model"
	category "FileEngine/biz/model/api/category"
	execute "FileEngine/biz/model/api/execute"
	runmodel "FileEngine/biz/model/api/run"
	session "FileEngine/biz/model/api/session"
	sessionsvc "FileEngine/biz/service/session"
	"FileEngine/internal/executor"
)

func toSession(v *sessionsvc.View) *session.Session {
	s := v.ScanSession
	out := &session.Session{
		ID: int64(s.ID), FilesystemID: int64(s.FilesystemID), ScanPath: s.ScanPath, RootPath: s.RootPath, Protocol: s.Protocol,
		Status: s.Status, TotalFiles: int32(s.TotalFiles), ScannedCount: int32(s.ScannedCount), CurrentPath: s.CurrentPath,
		ErrorMessage: s.ErrorMessage, ScanWarning: s.ScanWarning, ScanPhase: s.ScanPhase, PhaseDone: s.PhaseDone,
		PhaseTotal: s.PhaseTotal, TaggedFiles: int32(s.TaggedFiles), PlannedOps: int32(s.PlannedOps),
		ReviewCount: int32(s.ReviewCount), ExecutedOps: int32(s.ExecutedOps), TotalSize: s.TotalSize,
		AllowReadFile: s.AllowReadFile, AllowAutoCategory: s.AllowAutoCategory, AllowVision: s.AllowVision,
		AllowScript: s.AllowScript, ExtractMetadata: s.ExtractMetadata, DetectDuplicates: s.DetectDuplicates,
		ExcludeCategoryDirs: s.ExcludeCategoryDirs, FilterMode: s.FilterMode, FilterDirs: s.FilterDirs, Goal: s.Goal,
		ScopeExts: s.ScopeExts, ModelProviderID: int64(s.ModelProviderID), BulkModelProviderID: int64(s.BulkModelProviderID),
		CreatedAt: unix(s.CreatedAt), UpdatedAt: unix(s.UpdatedAt),
	}
	if r := v.LatestRun; r != nil {
		out.LatestRun = &session.RunRef{ID: int64(r.ID), Phase: r.Phase, Status: r.Status}
	}
	if t := v.Tokens; t != nil {
		out.Tokens = &runmodel.TokenCounts{
			Prompt: int32(t.Prompt), Cached: int32(t.Cached), CacheWrite: int32(t.CacheWrite),
			Completion: int32(t.Completion), Total: int32(t.Total),
		}
	}
	return out
}

// firstBad is the first non-nil error; the id parsers return *errs.Error, which
// errors.Join would hide from resp.Fail.
func firstBad(es ...error) error {
	for _, e := range es {
		if e != nil {
			return e
		}
	}
	return nil
}

// optUintRef converts an optional id field: absent stays nil.
func optUintRef(v *int64) (*uint, error) {
	if v == nil {
		return nil, nil
	}
	n, err := optID(*v)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func draftStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func draftNum[T ~int32 | ~int64](p *T) int64 {
	if p == nil {
		return 0
	}
	return int64(*p)
}

func toRuleDrafts(in []*session.SessionRuleDraft) []sessionsvc.RuleDraft {
	out := make([]sessionsvc.RuleDraft, len(in))
	for i, d := range in {
		if d == nil {
			continue
		}
		out[i] = sessionsvc.RuleDraft{
			Name: draftStr(d.Name), Exts: draftStr(d.Exts), NameRegex: draftStr(d.NameRegex), PathPrefix: draftStr(d.PathPrefix),
			FileType: draftStr(d.FileType), Target: draftStr(d.Target), Role: draftStr(d.Role), Source: draftStr(d.Source),
			MinSize: draftNum(d.MinSize), MaxSize: draftNum(d.MaxSize), Position: int(draftNum(d.Position)),
		}
	}
	return out
}

func toCategoryDrafts(in []*category.CategoryTemplate) []sessionsvc.CategoryDraft {
	out := make([]sessionsvc.CategoryDraft, len(in))
	for i, d := range in {
		if d == nil {
			continue
		}
		out[i] = sessionsvc.CategoryDraft{Name: d.Name, Path: d.Path, Structure: d.Structure, Description: d.Description}
	}
	return out
}

func toRun(r *model.AgentRun) *runmodel.Run {
	rev := func(id *uint) int64 {
		if id == nil {
			return 0
		}
		return int64(*id)
	}
	return &runmodel.Run{
		ID: int64(r.ID), SessionID: int64(r.SessionID), Phase: r.Phase, Status: r.Status, Title: r.Title, ExecMode: r.ExecMode,
		RevertsRunID: rev(r.RevertsRunID), RevertedByRunID: rev(r.RevertedByRunID), Summary: r.Summary, Error: r.Error,
		PromptTokens: int32(r.PromptTokens), CachedTokens: int32(r.CachedTokens), CacheWriteTokens: int32(r.CacheWriteTokens),
		CacheHitRate: r.CacheHitRate, CompletionTokens: int32(r.CompletionTokens), TotalTokens: int32(r.TotalTokens),
		StartedAt: unix(r.StartedAt), EndedAt: unixPtr(r.EndedAt),
		ExecTotal: int32(r.ExecTotal), ExecSuccess: int32(r.ExecSuccess), ExecFailed: int32(r.ExecFailed), ExecSkipped: int32(r.ExecSkipped),
	}
}

func toCheckResp(res *executor.Result) *execute.CheckExecuteResp {
	issues := make([]*execute.Issue, len(res.Issues))
	for i, is := range res.Issues {
		issues[i] = &execute.Issue{EntryID: int64(is.EntryID), OriginalPath: is.OriginalPath, NewPath: is.NewPath, Code: is.Code, Detail: is.Detail}
	}
	notes := res.Notes
	if notes == nil {
		notes = []string{}
	}
	return &execute.CheckExecuteResp{
		Total: int32(res.Total), Success: int32(res.Success), Failed: int32(res.Failed), Skipped: int32(res.Skipped),
		Issues: issues, Notes: notes,
	}
}

func toLocateItems(items []executor.LocateItem) []*execute.LocateItem {
	out := make([]*execute.LocateItem, len(items))
	for i, it := range items {
		out[i] = &execute.LocateItem{
			EntryID: int64(it.EntryID), OriginalPath: it.OriginalPath, Name: it.Name, FileType: it.FileType, NewPath: it.NewPath,
			DecidedBy: it.DecidedBy, Executed: it.Executed, ExecutedAt: unixPtr(it.ExecutedAt), ExecuteMode: it.ExecuteMode,
			OpState: it.OpState,
		}
	}
	return out
}
