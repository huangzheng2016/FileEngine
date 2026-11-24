namespace go api.event

// seq is the stream sequence (the SSE id) and stays a plain number so clients
// can compare it.
struct SessionEvent {
    1: i64 seq
    2: i64 run_id (go.tag='json:"run_id,string"')
    // run_status | run_done | pull_begin | pull_done | message | text | reasoning
    // | tool_call | tool_result | progress | plan_updated | memory_updated | session_updated
    3: string kind
    // the payload as a JSON document (the same bytes as the SSE data field)
    4: string data
    // unix seconds
    5: i64 created_at
}

struct ListSessionEventsReq {
    1: i64 session_id (api.query="session_id")
    // 0 = every run
    2: i64 run_id (api.query="run_id")
    // events with seq greater than after
    3: i64 after (api.query="after")
    // events with seq lower than before; 0 = unbounded (with only before set:
    // the newest events below it)
    4: i64 before (api.query="before")
    5: i32 limit (api.query="limit")
}

struct ListSessionEventsResp {
    1: i32 code
    2: string msg
    3: list<SessionEvent> items
    4: bool has_more
}

// text/event-stream. Frames: "id: <seq>\nevent: <kind>\ndata: <json>\n\n", plus a
// ": ping" comment as heartbeat. Resume with the Last-Event-ID header or `after`;
// with neither the stream starts at the latest run's first event.
struct StreamSessionEventsReq {
    1: i64 session_id (api.query="session_id")
    // 0 = every run
    2: i64 run_id (api.query="run_id")
    3: i64 after (api.query="after")
}
