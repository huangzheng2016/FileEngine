package api

import (
	"FileEngine/biz/model/api/config"
	"FileEngine/biz/service/settings"
	appconfig "FileEngine/internal/config"
)

func toAgentConfig(a appconfig.AgentConfig) *config.AgentConfig {
	return &config.AgentConfig{
		VisionMaxImagesPerBatch: int32(a.VisionMaxImagesPerBatch), VisionImageMaxPx: int32(a.VisionImageMaxPx),
		VisionMaxImageBytes: int32(a.VisionMaxImageBytes), Concurrency: int32(a.Concurrency),
		MaxFileReadSize: int32(a.MaxFileReadSize), ArchiveEntryMaxSize: int32(a.ArchiveEntryMaxSize),
		MaxRetries: int32(a.MaxRetries), RateLimitMaxRetries: int32(a.RateLimitMaxRetries),
		RetryBaseDelay: int32(a.RetryBaseDelay), RetryMaxDelay: int32(a.RetryMaxDelay),
		MaxStep: int32(a.MaxStep), ChatMaxStep: int32(a.ChatMaxStep), SystemPrompt: a.SystemPrompt,
		AgentTimeout: int32(a.AgentTimeout), ScriptMaxSize: int32(a.ScriptMaxSize),
		ScriptTimeout: int32(a.ScriptTimeout), ScriptMaxResultSize: int32(a.ScriptMaxResultSize),
		JunkFilterDisabled: a.JunkFilterDisabled, ArchivePromptDisabled: a.ArchivePromptDisabled,
		ArchivePromptMaxEntries: int32(a.ArchivePromptMaxEntries), CategoryPolicy: a.CategoryPolicy,
	}
}

func setInt(dst *int, src *int32) {
	if src != nil {
		*dst = int(*src)
	}
}

func setVal[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// applyPatch returns the change a patch makes; absent fields stay as they are.
func applyPatch(p *config.AgentConfigPatch) func(*appconfig.AgentConfig) {
	return func(a *appconfig.AgentConfig) {
		if p == nil {
			return
		}
		setInt(&a.VisionMaxImagesPerBatch, p.VisionMaxImagesPerBatch)
		setInt(&a.VisionImageMaxPx, p.VisionImageMaxPx)
		setInt(&a.VisionMaxImageBytes, p.VisionMaxImageBytes)
		setInt(&a.Concurrency, p.Concurrency)
		setInt(&a.MaxFileReadSize, p.MaxFileReadSize)
		setInt(&a.ArchiveEntryMaxSize, p.ArchiveEntryMaxSize)
		setInt(&a.MaxRetries, p.MaxRetries)
		setInt(&a.RateLimitMaxRetries, p.RateLimitMaxRetries)
		setInt(&a.RetryBaseDelay, p.RetryBaseDelay)
		setInt(&a.RetryMaxDelay, p.RetryMaxDelay)
		setInt(&a.MaxStep, p.MaxStep)
		setInt(&a.ChatMaxStep, p.ChatMaxStep)
		setVal(&a.SystemPrompt, p.SystemPrompt)
		setInt(&a.AgentTimeout, p.AgentTimeout)
		setInt(&a.ScriptMaxSize, p.ScriptMaxSize)
		setInt(&a.ScriptTimeout, p.ScriptTimeout)
		setInt(&a.ScriptMaxResultSize, p.ScriptMaxResultSize)
		setVal(&a.JunkFilterDisabled, p.JunkFilterDisabled)
		setVal(&a.ArchivePromptDisabled, p.ArchivePromptDisabled)
		setInt(&a.ArchivePromptMaxEntries, p.ArchivePromptMaxEntries)
		setVal(&a.CategoryPolicy, p.CategoryPolicy)
	}
}

func toConfigResp(s settings.Snapshot) *config.ConfigResp {
	cfg := appconfig.Get()
	return &config.ConfigResp{
		Server:                &config.ServerInfo{Host: cfg.Server.Host, Port: int32(cfg.Server.Port)},
		Database:              &config.DatabaseInfo{Driver: cfg.Database.Driver},
		Agent:                 toAgentConfig(s.Agent),
		DefaultCategoryPolicy: appconfig.DefaultCategoryPolicy,
		Version:               s.Version,
	}
}

func toPromptResp(s settings.Snapshot) *config.PromptResp {
	prompt, custom := settings.Prompt(s.Agent)
	return &config.PromptResp{Prompt: prompt, DefaultPrompt: settings.DefaultPrompt(), IsCustom: custom, Version: s.Version}
}
