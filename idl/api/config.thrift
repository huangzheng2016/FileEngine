namespace go api.config

// Seconds and counts as in config.yaml; see internal/config.AgentConfig.
struct AgentConfig {
    1: i32 vision_max_images_per_batch
    2: i32 vision_image_max_px
    3: i32 vision_max_image_bytes
    4: i32 concurrency
    5: i32 max_file_read_size
    6: i32 archive_entry_max_size
    7: i32 max_retries
    8: i32 rate_limit_max_retries
    9: i32 retry_base_delay
    10: i32 retry_max_delay
    11: i32 max_step
    12: i32 chat_max_step
    13: string system_prompt
    // seconds; <= 0 = no timeout
    14: i32 agent_timeout
    15: i32 script_max_size
    16: i32 script_timeout
    17: i32 script_max_result_size
    18: bool junk_filter_disabled
    19: bool archive_prompt_disabled
    20: i32 archive_prompt_max_entries
    21: string category_policy
}

// Absent fields stay unchanged.
struct AgentConfigPatch {
    1: optional i32 vision_max_images_per_batch
    2: optional i32 vision_image_max_px
    3: optional i32 vision_max_image_bytes
    4: optional i32 concurrency
    5: optional i32 max_file_read_size
    6: optional i32 archive_entry_max_size
    7: optional i32 max_retries
    8: optional i32 rate_limit_max_retries
    9: optional i32 retry_base_delay
    10: optional i32 retry_max_delay
    11: optional i32 max_step
    12: optional i32 chat_max_step
    13: optional string system_prompt
    14: optional i32 agent_timeout
    15: optional i32 script_max_size
    16: optional i32 script_timeout
    17: optional i32 script_max_result_size
    18: optional bool junk_filter_disabled
    19: optional bool archive_prompt_disabled
    20: optional i32 archive_prompt_max_entries
    21: optional string category_policy
}

struct ServerInfo {
    1: string host
    2: i32 port
}

struct DatabaseInfo {
    1: string driver
}

struct ConfigResp {
    1: i32 code
    2: string msg
    3: ServerInfo server
    4: DatabaseInfo database
    5: AgentConfig agent
    // the built-in category policy, shown when category_policy is empty
    6: string default_category_policy
    // saved version of the agent settings; 0 = never saved (defaults)
    7: i64 version
}

// Agent settings and the system prompt are one versioned document. A save
// names the version it was edited from; a newer one answers 409.
struct UpdateConfigReq {
    1: AgentConfigPatch agent
    2: i64 base_version
}

struct ConfigVersion {
    1: i64 version
    // unix seconds
    2: i64 saved_at
    // names of the settings that differ from the previous version
    3: list<string> changed
}

struct ListConfigVersionsReq {
    1: i32 page (api.query="page")
    2: i32 page_size (api.query="page_size")
}

// Newest first.
struct ListConfigVersionsResp {
    1: i32 code
    2: string msg
    3: list<ConfigVersion> items
    4: i64 total
}

struct PromptResp {
    1: i32 code
    2: string msg
    // the effective system prompt
    3: string prompt
    4: string default_prompt
    // false when the default is in use
    5: bool is_custom
    // same version stream as the agent settings
    6: i64 version
}

// A prompt equal to the default resets to it.
struct UpdatePromptReq {
    1: string prompt
    2: i64 base_version
}
