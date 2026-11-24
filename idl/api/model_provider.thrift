namespace go api.model_provider

struct ModelProvider {
    1: i64 id (go.tag='json:"id,string"')
    2: string name
    // openai | claude | ollama
    3: string provider
    4: string model
    5: string base_url
    6: double temperature
    7: i32 max_tokens
    // tokens; already the effective value (the default when unset)
    8: i32 context_window
    9: bool vision_enabled
    10: bool thinking_enabled
    // "" = model default
    11: string reasoning_effort
    12: bool api_key_set
    13: i64 created_at
    14: i64 updated_at
}

struct ListModelProvidersReq {}

struct ListModelProvidersResp {
    1: i32 code
    2: string msg
    3: list<ModelProvider> items
    // not paged: total == len(items)
    4: i64 total
}

struct GetModelProviderReq {
    1: i64 id (api.query="id")
}

struct ModelProviderResp {
    1: i32 code
    2: string msg
    3: ModelProvider item
}

// api_key: absent keeps the stored key, "" clears it. Absent optional fields
// keep the stored value (off / default on create).
struct SaveModelProviderReq {
    // 0 on create
    1: i64 id (go.tag='json:"id,string"')
    2: string name (api.vd="len($) > 0")
    3: string provider (api.vd="len($) > 0")
    4: string model (api.vd="len($) > 0")
    5: string base_url
    6: double temperature
    7: i32 max_tokens
    8: optional string api_key
    9: optional i32 context_window
    10: optional bool vision_enabled
    11: optional bool thinking_enabled
    12: optional string reasoning_effort
}

struct ModelProviderIDReq {
    1: i64 id (go.tag='json:"id,string"')
}

// Same fields as a save; id != 0 reuses that provider's stored key for the same endpoint.
struct TestModelProviderReq {
    1: i64 id (go.tag='json:"id,string"')
    2: string provider (api.vd="len($) > 0")
    3: string model
    4: string base_url
    5: double temperature
    6: i32 max_tokens
    7: optional string api_key
    8: optional bool thinking_enabled
    9: optional string reasoning_effort
    10: optional i32 context_window
}

// A failed test is a normal answer: success=false with error set.
struct TestModelProviderResp {
    1: i32 code
    2: string msg
    3: bool success
    4: string reply
    5: string error
    6: string detail
}

struct ListProviderModelsReq {
    1: i64 id (go.tag='json:"id,string"')
    2: string provider (api.vd="len($) > 0")
    3: string base_url
    4: optional string api_key
}

// A failed listing is a normal answer: success=false with error set.
struct ListProviderModelsResp {
    1: i32 code
    2: string msg
    3: bool success
    4: list<string> models
    5: string error
    6: string detail
}
