namespace go api.file

struct FileEntry {
    1: i64 id (go.tag='json:"id,string"')
    2: i64 session_id (go.tag='json:"session_id,string"')
    3: string original_path
    // "/"-rooted; empty without a target
    4: string new_path
    // move | copy | empty
    5: string operation
    // "" (undecided) | unit | container | member | file | skip | duplicate
    6: string role
    7: string reason
    // 0 = not given
    8: double confidence
    9: bool needs_review
    10: bool sensitive
    11: bool executed
    // unix seconds; 0 = not executed
    12: i64 executed_at
    13: string execute_mode
    14: string name
    15: i64 size
    // unix seconds
    16: i64 mod_time
    17: string permissions
    // file | directory | symlink
    18: string file_type
    19: string description
    20: bool tagged
    // "" for the children of the scan root
    21: string parent_path
    22: i32 depth
    23: i32 child_count
    24: i32 subdir_count
    25: i32 batch_index
    // unix seconds of the naive wall-clock capture time stored as UTC; 0 = unknown
    26: i64 taken_at
    27: string camera
    28: bool has_gps
    // exif | video | filename | mtime; empty = not extracted
    29: string date_source
    30: string hash
    // 0 = not a duplicate
    31: i64 duplicate_of (go.tag='json:"duplicate_of,string"')
    32: i64 created_at
    33: i64 updated_at
}

struct ListFilesReq {
    1: i64 session_id (api.query="session_id")
    // present and empty = the children of the scan root; absent = no folder filter
    2: optional string parent_path (api.query="parent_path")
    // file | directory | symlink; empty = any
    3: string file_type (api.query="file_type")
    4: optional bool tagged (api.query="tagged")
    5: optional bool categorized (api.query="categorized")
    6: optional string category_path (api.query="category_path")
    7: string search (api.query="search")
    8: optional bool needs_review (api.query="needs_review")
    9: optional bool sensitive (api.query="sensitive")
    10: optional string role (api.query="role")
    11: i32 page (api.query="page")
    // clamped to 200
    12: i32 page_size (api.query="page_size")
}

struct ListFilesResp {
    1: i32 code
    2: string msg
    3: list<FileEntry> items
    4: i64 total
    // decisions with a confidence below this wait for the user's confirmation
    5: double review_confidence
}

struct GetFileReq {
    1: i64 id (api.query="id")
}

struct FileResp {
    1: i32 code
    2: string msg
    3: FileEntry item
}

// Absent fields stay unchanged. new_path is a user decision on the entry.
struct UpdateFileReq {
    1: i64 id (go.tag='json:"id,string"')
    2: optional string description
    3: optional string new_path
}

struct GetFileTreeReq {
    1: i64 session_id (api.query="session_id")
    // empty = the scan root
    2: string parent_path (api.query="parent_path")
}

struct TreeNode {
    1: i64 id (go.tag='json:"id,string"')
    2: string label
    3: string original_path
    4: string file_type
    5: i32 child_count
    6: bool tagged
    7: bool is_leaf
}

struct GetFileTreeResp {
    1: i32 code
    2: string msg
    3: list<TreeNode> items
}

// The file's bytes as an inline attachment instead of JSON.
struct GetFileContentReq {
    1: i64 id (api.query="id")
}
