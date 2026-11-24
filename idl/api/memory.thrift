namespace go api.memory

// Session items are working notes shared by the session's workers; filesystem
// and global items persist across sessions.
struct MemoryItem {
    1: i64 id (go.tag='json:"id,string"')
    // session | filesystem | global
    2: string scope
    // the session or storage id for those scopes; 0 for global
    3: i64 scope_id (go.tag='json:"scope_id,string"')
    // preference | convention | correction | fact | note
    4: string kind
    // normalized subject; one live item per (scope, scope_id, key)
    5: string key
    6: string content
    7: string path
    // user | agent | correction | reflection
    8: string source
    9: bool pinned
    10: i64 created_at
    11: i64 updated_at
}

struct ListMemoryReq {
    // empty = every scope
    1: string scope (api.query="scope")
    // absent = every scope id
    2: optional i64 scope_id (api.query="scope_id")
}

struct ListMemoryResp {
    1: i32 code
    2: string msg
    3: list<MemoryItem> items
    // not paged: total == len(items)
    4: i64 total
}

struct MemoryResp {
    1: i32 code
    2: string msg
    3: MemoryItem item
    // create only: false when an existing item with the same key was updated
    4: bool created
}

struct CreateMemoryReq {
    1: string scope
    2: i64 scope_id (go.tag='json:"scope_id,string"')
    3: string kind
    4: string key
    5: string content
    6: string path
    7: bool pinned
}

// Absent fields stay unchanged.
struct UpdateMemoryReq {
    1: i64 id (go.tag='json:"id,string"')
    2: optional string kind
    3: optional string key
    4: optional string content
    5: optional string path
    6: optional bool pinned
}

struct MemoryIDReq {
    1: i64 id (go.tag='json:"id,string"')
}
