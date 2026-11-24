include "base.thrift"
include "api/auth.thrift"
include "api/filesystem.thrift"
include "api/model_provider.thrift"
include "api/session.thrift"
include "api/run.thrift"
include "api/event.thrift"
include "api/chat.thrift"
include "api/execute.thrift"
include "api/plan.thrift"
include "api/rule.thrift"
include "api/memory.thrift"
include "api/file.thrift"
include "api/category.thrift"
include "api/config.thrift"

namespace go api

// Conventions for every route below:
//  - success is HTTP 200 with code 0; every failure is {code, msg} with the
//    status of its biz/errs kind and a Chinese msg
//  - a start-run route answers run.StartRunResp, a delete or other payload-free
//    write answers base.EmptyResp
//  - routes marked "raw" answer a file or a redirect instead of JSON
//  - unix timestamps are seconds, 0 = none; ids of 0 mean "none" unless noted

service AuthService {
    auth.AuthResp Login(1: auth.LoginReq req) (api.post="/api/auth/login")
    auth.AuthResp Logout(1: base.EmptyReq req) (api.post="/api/auth/logout")
    auth.MeResp GetMe(1: base.EmptyReq req) (api.get="/api/auth/me")
    // raw: sets the session cookie and redirects to "/"
    base.EmptyResp Bootstrap(1: auth.BootstrapReq req) (api.get="/api/auth/bootstrap")
}

service AdminService {
    // raw: a consistent SQLite snapshot as an attachment
    base.EmptyResp BackupDatabase(1: base.EmptyReq req) (api.post="/api/admin/backup")
}

service FilesystemService {
    filesystem.ListFilesystemsResp ListFilesystems(1: filesystem.ListFilesystemsReq req) (api.get="/api/filesystem/list")
    filesystem.FilesystemResp GetFilesystem(1: filesystem.GetFilesystemReq req) (api.get="/api/filesystem/get")
    filesystem.FilesystemResp CreateFilesystem(1: filesystem.SaveFilesystemReq req) (api.post="/api/filesystem/create")
    filesystem.FilesystemResp UpdateFilesystem(1: filesystem.SaveFilesystemReq req) (api.post="/api/filesystem/update")
    base.EmptyResp DeleteFilesystem(1: filesystem.FilesystemIDReq req) (api.post="/api/filesystem/delete")
    filesystem.TestFilesystemResp TestFilesystem(1: filesystem.TestFilesystemReq req) (api.post="/api/filesystem/test")
}

service ModelProviderService {
    model_provider.ListModelProvidersResp ListModelProviders(1: model_provider.ListModelProvidersReq req) (api.get="/api/model/list")
    model_provider.ModelProviderResp GetModelProvider(1: model_provider.GetModelProviderReq req) (api.get="/api/model/get")
    model_provider.ModelProviderResp CreateModelProvider(1: model_provider.SaveModelProviderReq req) (api.post="/api/model/create")
    model_provider.ModelProviderResp UpdateModelProvider(1: model_provider.SaveModelProviderReq req) (api.post="/api/model/update")
    base.EmptyResp DeleteModelProvider(1: model_provider.ModelProviderIDReq req) (api.post="/api/model/delete")
    model_provider.TestModelProviderResp TestModelProvider(1: model_provider.TestModelProviderReq req) (api.post="/api/model/test")
    model_provider.ListProviderModelsResp ListProviderModels(1: model_provider.ListProviderModelsReq req) (api.post="/api/model/catalog")
}

service SessionService {
    session.SessionResp CreateSession(1: session.CreateSessionReq req) (api.post="/api/session/create")
    session.ListSessionsResp ListSessions(1: session.ListSessionsReq req) (api.get="/api/session/list")
    session.SessionResp GetSession(1: session.GetSessionReq req) (api.get="/api/session/get")
    session.SessionResp UpdateSession(1: session.UpdateSessionReq req) (api.post="/api/session/update")
    base.EmptyResp DeleteSession(1: session.SessionIDReq req) (api.post="/api/session/delete")
    session.SessionResp RescanSession(1: session.SessionIDReq req) (api.post="/api/session/rescan")
    session.SessionStatsResp GetSessionStats(1: session.SessionStatsReq req) (api.get="/api/session/stats")
}

service RunService {
    run.StartRunResp StartTagging(1: run.StartTaggingReq req) (api.post="/api/run/tag")
    run.ListSessionRunsResp ListSessionRuns(1: run.ListSessionRunsReq req) (api.get="/api/run/list")
    run.RunResp GetRun(1: run.GetRunReq req) (api.get="/api/run/get")
    run.RunResp StopRun(1: run.StopRunReq req) (api.post="/api/run/stop")
}

service EventService {
    event.ListSessionEventsResp ListSessionEvents(1: event.ListSessionEventsReq req) (api.get="/api/event/list")
    // raw: text/event-stream; streaming routes end in /stream
    base.EmptyResp StreamSessionEvents(1: event.StreamSessionEventsReq req) (api.get="/api/event/stream")
}

service ChatService {
    run.StartRunResp SendChat(1: chat.SendChatReq req) (api.post="/api/chat/send")
    chat.ListChatTurnsResp ListChatTurns(1: chat.ListChatTurnsReq req) (api.get="/api/chat/list")
}

service ExecuteService {
    run.StartRunResp StartExecute(1: execute.StartExecuteReq req) (api.post="/api/execute/start")
    execute.CheckExecuteResp CheckExecute(1: execute.CheckExecuteReq req) (api.get="/api/execute/check")
    run.StartRunResp RevertRun(1: execute.RevertRunReq req) (api.post="/api/execute/revert")
    execute.LocateEntriesResp LocateEntries(1: execute.LocateEntriesReq req) (api.get="/api/execute/locate")
}

service PlanService {
    plan.PlanResp GetPlan(1: plan.GetPlanReq req) (api.get="/api/plan/get")
    plan.PlanResp UpdatePlan(1: plan.UpdatePlanReq req) (api.post="/api/plan/update")
    plan.PlanResp ApprovePlan(1: plan.ApprovePlanReq req) (api.post="/api/plan/approve")
    run.StartRunResp DraftPlan(1: plan.DraftPlanReq req) (api.post="/api/plan/draft")
    plan.ListPlanOpsResp ListPlanOps(1: plan.ListPlanOpsReq req) (api.get="/api/plan/ops")
    // raw: text/csv attachment
    base.EmptyResp ExportPlanOps(1: plan.ExportPlanOpsReq req) (api.get="/api/plan/ops_export")
    plan.ResetPlanOpsResp ResetPlanOps(1: plan.ResetPlanOpsReq req) (api.post="/api/plan/ops_reset")
    plan.PreviewTreeResp PreviewTree(1: plan.PreviewTreeReq req) (api.get="/api/plan/preview")
    plan.ConfirmReviewResp ConfirmReview(1: plan.ConfirmReviewReq req) (api.post="/api/plan/confirm_review")
}

service RuleService {
    rule.ListRulesResp ListRules(1: rule.ListRulesReq req) (api.get="/api/rule/list")
    rule.RuleResp CreateRule(1: rule.CreateRuleReq req) (api.post="/api/rule/create")
    rule.RuleResp UpdateRule(1: rule.UpdateRuleReq req) (api.post="/api/rule/update")
    base.EmptyResp DeleteRule(1: rule.RuleIDReq req) (api.post="/api/rule/delete")
    rule.ApplyRulesResp ApplyRules(1: rule.ApplyRulesReq req) (api.post="/api/rule/apply")
}

service MemoryService {
    memory.ListMemoryResp ListMemory(1: memory.ListMemoryReq req) (api.get="/api/memory/list")
    memory.MemoryResp CreateMemory(1: memory.CreateMemoryReq req) (api.post="/api/memory/create")
    memory.MemoryResp UpdateMemory(1: memory.UpdateMemoryReq req) (api.post="/api/memory/update")
    base.EmptyResp DeleteMemory(1: memory.MemoryIDReq req) (api.post="/api/memory/delete")
}

service FileService {
    file.ListFilesResp ListFiles(1: file.ListFilesReq req) (api.get="/api/file/list")
    file.FileResp GetFile(1: file.GetFileReq req) (api.get="/api/file/get")
    file.FileResp UpdateFile(1: file.UpdateFileReq req) (api.post="/api/file/update")
    file.GetFileTreeResp GetFileTree(1: file.GetFileTreeReq req) (api.get="/api/file/tree")
    // raw: the file's bytes
    base.EmptyResp GetFileContent(1: file.GetFileContentReq req) (api.get="/api/file/content")
}

service CategoryService {
    category.ListCategoriesResp ListCategories(1: category.ListCategoriesReq req) (api.get="/api/category/list")
    category.CategoryResp CreateCategory(1: category.SaveCategoryReq req) (api.post="/api/category/create")
    category.CategoryResp UpdateCategory(1: category.SaveCategoryReq req) (api.post="/api/category/update")
    base.EmptyResp DeleteCategory(1: category.CategoryIDReq req) (api.post="/api/category/delete")
    category.ExportCategoriesResp ExportCategories(1: category.ExportCategoriesReq req) (api.get="/api/category/export")
    category.ImportCategoriesResp ImportCategories(1: category.ImportCategoriesReq req) (api.post="/api/category/import")
}

service ConfigService {
    config.ConfigResp GetConfig(1: base.EmptyReq req) (api.get="/api/config/get")
    config.ConfigResp UpdateConfig(1: config.UpdateConfigReq req) (api.post="/api/config/update")
    config.PromptResp GetPrompt(1: base.EmptyReq req) (api.get="/api/config/prompt")
    config.PromptResp UpdatePrompt(1: config.UpdatePromptReq req) (api.post="/api/config/prompt_update")
}
