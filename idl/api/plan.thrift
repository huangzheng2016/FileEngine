namespace go api.plan

struct SessionPlan {
    1: i64 session_id (go.tag='json:"session_id,string"')
    // 0 = no plan written yet
    2: i32 version
    // draft | approved
    3: string status
    4: string content
    // unix seconds
    5: i64 updated_at
    // unix seconds; 0 = not approved
    6: i64 approved_at
}

struct GetPlanReq {
    1: i64 session_id (api.query="session_id")
}

struct PlanResp {
    1: i32 code
    2: string msg
    3: SessionPlan item
}

// A save from a stale version answers 409 and the client re-reads with GetPlan.
struct UpdatePlanReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    2: string content
    // the version the editor was loaded from
    3: i32 version (api.vd="$ >= 0")
}

struct ApprovePlanReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
}

// Starts a survey run (answer with run.StartRunResp).
struct DraftPlanReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    2: string instructions
}

// The dry-run operation list of the decided entries.
struct ListPlanOpsReq {
    1: i64 session_id (api.query="session_id")
}

struct PlanOp {
    1: i64 file_id (go.tag='json:"file_id,string"')
    2: string original_path
    3: string new_path
    // move | copy
    4: string operation
    5: string name
    6: string file_type
}

struct ListPlanOpsResp {
    1: i32 code
    2: string msg
    3: list<PlanOp> items
    // not paged: total == len(items)
    4: i64 total
}

// text/csv attachment (UTF-8 with BOM) instead of JSON.
struct ExportPlanOpsReq {
    1: i64 session_id (api.query="session_id")
}

struct ResetPlanOpsReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    // ids as strings: the json ",string" option does not apply to lists
    2: list<string> file_ids (api.vd="len($) > 0")
}

struct ResetPlanOpsResp {
    1: i32 code
    2: string msg
    3: i32 reset
}

struct PreviewTreeReq {
    1: i64 session_id (api.query="session_id")
    // folder to list; empty = the scan root
    2: string path (api.query="path")
    // after | before
    3: string side (api.query="side")
    4: i32 offset (api.query="offset")
    // 1..1000, 200 when 0
    5: i32 limit (api.query="limit")
}

struct PreviewItem {
    1: string name
    2: string path
    3: bool is_dir
    4: i64 file_count
    5: i64 size
    // 0 = not a single entry
    6: i64 file_id (go.tag='json:"file_id,string"')
    7: string original_path
    8: string status
    9: string new_path
    10: i64 moved_count
    11: string role
    12: string reason
    13: bool needs_review
    // 0 = not given
    14: double confidence
    15: bool sensitive
}

struct PreviewTreeResp {
    1: i32 code
    2: string msg
    3: list<PreviewItem> items
    4: i64 total
}

struct ConfirmReviewReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    2: list<string> file_ids (api.vd="len($) > 0")
}

struct ConfirmReviewResp {
    1: i32 code
    2: string msg
    3: i32 confirmed
}
