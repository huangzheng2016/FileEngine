namespace go api.chat

struct ChatTurn {
    1: i64 id (go.tag='json:"id,string"')
    2: i64 session_id (go.tag='json:"session_id,string"')
    3: i64 run_id (go.tag='json:"run_id,string"')
    // user | assistant
    4: string role
    5: string content
    // unix seconds
    6: i64 created_at
}

// Starts a chat run (answer with run.StartRunResp); the reply streams as events.
struct SendChatReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    2: string content (api.vd="len($) > 0")
    // files of this session the question is about, at most 50
    3: list<string> file_ids (api.vd="len($) <= 50")
}

struct ListChatTurnsReq {
    1: i64 session_id (api.query="session_id")
}

struct ListChatTurnsResp {
    1: i32 code
    2: string msg
    3: list<ChatTurn> items
    // not paged: total == len(items)
    4: i64 total
}
