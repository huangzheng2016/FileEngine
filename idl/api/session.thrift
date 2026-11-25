namespace go api.session

include "run.thrift"
include "category.thrift"

struct RunRef {
    1: i64 id (go.tag='json:"id,string"')
    2: string phase
    3: string status
}

struct Session {
    1: i64 id (go.tag='json:"id,string"')
    2: i64 filesystem_id (go.tag='json:"filesystem_id,string"')
    3: string scan_path
    4: string root_path
    5: string protocol
    // scanning | scanned | error | interrupted; agent activity is read from runs
    6: string status
    7: i32 total_files
    8: i32 scanned_count
    9: string current_path
    10: string error_message
    11: i32 tagged_files
    12: i32 planned_ops
    // planned, unexecuted entries waiting for confirmation
    13: i32 review_count
    14: i32 executed_ops
    15: i64 total_size
    16: bool allow_read_file
    17: bool allow_auto_category
    18: bool allow_vision
    19: bool allow_script
    20: bool extract_metadata
    21: bool detect_duplicates
    22: bool exclude_category_dirs
    // blacklist | whitelist
    23: string filter_mode
    // one folder per line
    24: string filter_dirs
    25: string goal
    // comma-separated extensions without dot; empty = every file
    26: string scope_exts
    27: i64 model_provider_id (go.tag='json:"model_provider_id,string"')
    // 0 = same as model_provider_id
    28: i64 bulk_model_provider_id (go.tag='json:"bulk_model_provider_id,string"')
    // unix seconds
    29: i64 created_at
    30: i64 updated_at
    // absent when the session has no run yet
    31: optional RunRef latest_run
    // token totals over the session's runs; only present in GetSession
    32: optional run.TokenCounts tokens
    // status scanned only: what the scan had to skip (for example unreadable
    // folders); empty when nothing was skipped. error_message is set only
    // when status is error or interrupted.
    33: string scan_warning
    // status scanning only: walk | metadata | duplicates | finishing; empty
    // otherwise
    34: string scan_phase
    // progress of scan_phase: files (metadata) or bytes (duplicates, where
    // phase_total is the most that may be read); phase_total 0 = unmeasured
    35: i64 phase_done
    36: i64 phase_total
}

// A template rule created together with the session; same fields and meaning as
// rule.CreateRuleReq without the session.
struct SessionRuleDraft {
    1: optional string name
    2: optional string exts
    3: optional string name_regex
    4: optional string path_prefix
    5: optional string file_type
    6: optional i64 min_size
    7: optional i64 max_size
    8: optional string target
    9: optional string role
    10: optional i32 position
    11: optional string source
}

// Absent optional fields take the default of a new task (extraction and the
// agent switches on, vision and duplicate detection off).
struct CreateSessionReq {
    1: i64 filesystem_id (go.tag='json:"filesystem_id,string"', api.vd="$ > 0")
    2: string scan_path
    3: i64 model_provider_id (go.tag='json:"model_provider_id,string"')
    // 0 = same as model_provider_id
    4: i64 bulk_model_provider_id (go.tag='json:"bulk_model_provider_id,string"')
    5: bool exclude_category_dirs
    // blacklist when empty
    6: string filter_mode
    7: string filter_dirs
    8: bool allow_vision
    9: optional bool extract_metadata
    10: bool detect_duplicates
    11: string goal
    // start tagging as soon as the scan completes
    12: bool auto_tag
    13: string scope_exts
    14: optional bool allow_read_file
    15: optional bool allow_auto_category
    16: optional bool allow_script
    // created in the same transaction as the session, before the scan starts; at
    // most 200 each, any invalid entry rejects the whole request
    17: optional list<SessionRuleDraft> rules
    // categories of the session's storage (existing names are kept as they are)
    18: optional list<category.CategoryTemplate> categories
}

struct SessionResp {
    1: i32 code
    2: string msg
    3: Session item
}

struct ListSessionsReq {
    // 0 = every storage
    1: i64 filesystem_id (api.query="filesystem_id")
}

struct ListSessionsResp {
    1: i32 code
    2: string msg
    3: list<Session> items
    // not paged: total == len(items)
    4: i64 total
}

struct GetSessionReq {
    1: i64 id (api.query="id")
}

struct SessionIDReq {
    1: i64 id (go.tag='json:"id,string"')
}

// Absent fields stay unchanged.
struct UpdateSessionReq {
    1: i64 id (go.tag='json:"id,string"')
    2: optional bool allow_vision
    3: optional bool allow_read_file
    4: optional bool allow_auto_category
    5: optional bool allow_script
    6: optional bool exclude_category_dirs
    7: optional string filter_mode
    8: optional string filter_dirs
    9: optional i64 model_provider_id (go.tag='json:"model_provider_id,string"')
    10: optional bool extract_metadata
    11: optional bool detect_duplicates
    12: optional i64 bulk_model_provider_id (go.tag='json:"bulk_model_provider_id,string"')
    13: optional string goal
    14: optional string scope_exts
}

struct SessionStatsReq {
    1: i64 id (api.query="id")
}

struct CategoryDistribution {
    1: string name
    2: string path
    3: i64 file_count
    4: i64 total_size
}

struct SessionStatsResp {
    1: i32 code
    2: string msg
    3: list<CategoryDistribution> category_distribution
    4: i32 total_files
    5: i32 tagged_files
    6: i32 planned_ops
    7: i32 total_tokens
}
