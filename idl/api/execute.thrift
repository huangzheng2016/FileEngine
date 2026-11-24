namespace go api.execute

// Starts an executing run (answer with run.StartRunResp).
struct StartExecuteReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    // copy | move
    2: string mode (api.vd="$ == 'copy' || $ == 'move'")
    // execute only the first N runnable entries; 0 = all
    3: i32 sample
    // execute only entries targeting this category path; empty = all
    4: string category_path
}

struct CheckExecuteReq {
    1: i64 session_id (api.query="session_id")
    // copy when empty
    2: string mode (api.query="mode")
    3: i32 sample (api.query="sample")
    4: string category_path (api.query="category_path")
}

struct Issue {
    1: i64 entry_id (go.tag='json:"entry_id,string"')
    2: string original_path
    3: string new_path
    // invalid_target | into_self | collision | target_exists
    // | source_changed | source_missing | failed
    4: string code
    5: string detail
}

struct CheckExecuteResp {
    1: i32 code
    2: string msg
    3: i32 total
    4: i32 success
    5: i32 failed
    6: i32 skipped
    7: list<Issue> issues
    8: list<string> notes
}

// Starts a revert run (answer with run.StartRunResp).
struct RevertRunReq {
    1: i64 run_id (go.tag='json:"run_id,string"')
}

struct LocateEntriesReq {
    1: i64 session_id (api.query="session_id")
    2: string q (api.query="q", api.vd="len($) > 0")
}

struct LocateItem {
    1: i64 entry_id (go.tag='json:"entry_id,string"')
    2: string original_path
    3: string name
    4: string file_type
    // effective target, "/"-rooted; empty = stays in place
    5: string new_path
    // original path of the deciding entry (itself or an ancestor)
    6: string decided_by
    7: bool executed
    // unix seconds; 0 = not executed
    8: i64 executed_at
    9: string execute_mode
    // latest journal state of the deciding entry
    10: string op_state
}

struct LocateEntriesResp {
    1: i32 code
    2: string msg
    3: list<LocateItem> items
    // not paged: total == len(items)
    4: i64 total
}
