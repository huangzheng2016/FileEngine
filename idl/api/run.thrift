namespace go api.run

struct TokenCounts {
    1: i32 prompt
    2: i32 cached
    3: i32 cache_write
    4: i32 completion
    5: i32 total
}

struct Run {
    1: i64 id (go.tag='json:"id,string"')
    2: i64 session_id (go.tag='json:"session_id,string"')
    // survey | tagging | review | chat | executing | revert
    3: string phase
    // running | done | partial | error | stopped
    4: string status
    5: string title
    // executing runs: copy | move; empty otherwise
    6: string exec_mode
    // 0 = not a revert run
    7: i64 reverts_run_id (go.tag='json:"reverts_run_id,string"')
    // 0 = not reverted
    8: i64 reverted_by_run_id (go.tag='json:"reverted_by_run_id,string"')
    9: string summary
    10: string error
    11: i32 prompt_tokens
    12: i32 cached_tokens
    13: i32 cache_write_tokens
    // cached / prompt, 0..1
    14: double cache_hit_rate
    15: i32 completion_tokens
    16: i32 total_tokens
    // unix seconds
    17: i64 started_at
    // unix seconds; 0 while running
    18: i64 ended_at
}

struct StartTaggingReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
}

// The answer of every call that starts a run.
struct StartRunResp {
    1: i32 code
    2: string msg
    3: i64 run_id (go.tag='json:"run_id,string"')
}

struct ListSessionRunsReq {
    1: i64 session_id (api.query="session_id")
}

struct ListSessionRunsResp {
    1: i32 code
    2: string msg
    3: list<Run> items
    // not paged: total == len(items)
    4: i64 total
}

struct GetRunReq {
    1: i64 run_id (api.query="run_id")
}

struct RunResp {
    1: i32 code
    2: string msg
    3: Run item
}

struct StopRunReq {
    1: i64 run_id (go.tag='json:"run_id,string"')
}
