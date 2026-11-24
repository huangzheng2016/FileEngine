namespace go api.rule

// Rules belong to the session plan and are re-applied after a rescan. A rule
// matches when all its non-empty conditions hold; the first match by position wins.
struct PlanRule {
    1: i64 id (go.tag='json:"id,string"')
    2: i64 session_id (go.tag='json:"session_id,string"')
    // lower runs first
    3: i32 position
    4: string name
    // comma-separated, lower-case, without dot
    5: string exts
    6: string name_regex
    7: string path_prefix
    // file | directory | empty
    8: string file_type
    // 0 = no bound
    9: i64 min_size
    10: i64 max_size
    // template variables: {name} {stem} {ext} {year} {month} {day} {parent} {rel} ...
    11: string target
    // file | unit
    12: string role
    // user | agent | preset
    13: string source
    // entries decided by this rule in the last apply
    14: i32 matched
    15: i64 created_at
    16: i64 updated_at
}

struct ListRulesReq {
    1: i64 session_id (api.query="session_id")
}

struct ListRulesResp {
    1: i32 code
    2: string msg
    3: list<PlanRule> items
    // not paged: total == len(items)
    4: i64 total
}

struct RuleResp {
    1: i32 code
    2: string msg
    3: PlanRule item
}

// Absent fields are empty on create and unchanged on update.
struct CreateRuleReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    2: optional string name
    3: optional string exts
    4: optional string name_regex
    5: optional string path_prefix
    6: optional string file_type
    7: optional i64 min_size
    8: optional i64 max_size
    9: optional string target
    10: optional string role
    11: optional i32 position
    12: optional string source
}

struct UpdateRuleReq {
    1: i64 id (go.tag='json:"id,string"')
    2: optional string name
    3: optional string exts
    4: optional string name_regex
    5: optional string path_prefix
    6: optional string file_type
    7: optional i64 min_size
    8: optional i64 max_size
    9: optional string target
    10: optional string role
    11: optional i32 position
    12: optional string source
}

struct RuleIDReq {
    1: i64 id (go.tag='json:"id,string"')
}

struct ApplyRulesReq {
    1: i64 session_id (go.tag='json:"session_id,string"')
    // report what would match without deciding anything
    2: bool preview
}

struct RuleSample {
    1: string original_path
    2: string new_path
}

struct RuleApplyStat {
    1: i64 rule_id (go.tag='json:"rule_id,string"')
    2: string name
    3: i32 matched
    // matched but not decided: bad target, or refused by the decision rules
    4: i32 skipped
    5: list<RuleSample> samples
}

struct ApplyRulesResp {
    1: i32 code
    2: string msg
    3: list<RuleApplyStat> items
    4: i32 applied
    5: i32 skipped
}
