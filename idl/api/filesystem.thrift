namespace go api.filesystem

// Secrets never leave the server: the *_set flags say whether one is stored.
struct Filesystem {
    1: i64 id (go.tag='json:"id,string"')
    2: string name
    3: string description
    // local | sftp | ftp | smb | nfs
    4: string protocol
    5: string base_path
    6: string host
    7: i32 port
    8: string username
    9: bool password_set
    10: bool private_key_set
    11: bool key_passphrase_set
    // SFTP pinned fingerprint (SHA256:...), empty before the first pin
    12: string host_key
    13: i64 created_at
    14: i64 updated_at
}

struct ListFilesystemsReq {}

struct ListFilesystemsResp {
    1: i32 code
    2: string msg
    3: list<Filesystem> items
    // not paged: total == len(items)
    4: i64 total
}

struct GetFilesystemReq {
    1: i64 id (api.query="id")
}

struct FilesystemResp {
    1: i32 code
    2: string msg
    3: Filesystem item
}

// For password, private_key, key_passphrase and host_key: absent keeps the
// stored value, "" clears it.
struct SaveFilesystemReq {
    // 0 on create
    1: i64 id (go.tag='json:"id,string"')
    2: string name (api.vd="len($) > 0")
    3: string description
    4: string protocol (api.vd="len($) > 0")
    5: string base_path
    6: string host
    7: i32 port
    8: string username
    9: optional string password
    10: optional string private_key
    11: optional string key_passphrase
    12: optional string host_key
}

struct FilesystemIDReq {
    1: i64 id (go.tag='json:"id,string"')
}

// Probe of unsaved settings, or of a stored storage (id != 0, whose secrets are
// reused only for the same endpoint). Also lists the folders under base_path
// for the folder picker.
struct TestFilesystemReq {
    1: i64 id (go.tag='json:"id,string"')
    2: string protocol (api.vd="len($) > 0")
    3: string base_path
    4: string host
    5: i32 port
    6: string username
    7: optional string password
    8: optional string private_key
    9: optional string key_passphrase
    10: optional string host_key
}

struct ProbeDir {
    1: string name
    2: string path
}

// A failed probe is a normal answer: success=false with error set.
struct TestFilesystemResp {
    1: i32 code
    2: string msg
    3: bool success
    4: string error
    5: string detail
    6: string host_key
    7: bool host_key_changed
    8: list<ProbeDir> dirs
}
