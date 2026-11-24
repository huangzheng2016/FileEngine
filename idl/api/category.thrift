namespace go api.category

struct Category {
    1: i64 id (go.tag='json:"id,string"')
    2: i64 filesystem_id (go.tag='json:"filesystem_id,string"')
    3: string name
    // "/"-rooted
    4: string path
    5: string structure
    6: string description
    7: bool agent_created
    8: bool agent_editable
    9: i64 created_at
    10: i64 updated_at
}

struct ListCategoriesReq {
    1: i64 filesystem_id (api.query="filesystem_id", api.vd="$ > 0")
}

struct ListCategoriesResp {
    1: i32 code
    2: string msg
    3: list<Category> items
    // not paged: total == len(items)
    4: i64 total
}

struct CategoryResp {
    1: i32 code
    2: string msg
    3: Category item
}

// filesystem_id cannot change on update.
struct SaveCategoryReq {
    // 0 on create
    1: i64 id (go.tag='json:"id,string"')
    2: i64 filesystem_id (go.tag='json:"filesystem_id,string"', api.vd="$ > 0")
    3: string name (api.vd="len($) > 0")
    4: string path (api.vd="len($) > 0")
    5: string structure
    6: string description
}

struct CategoryIDReq {
    1: i64 id (go.tag='json:"id,string"')
}

struct CategoryTemplate {
    1: string name
    2: string path
    3: string structure
    4: string description
}

struct ExportCategoriesReq {
    1: i64 filesystem_id (api.query="filesystem_id", api.vd="$ > 0")
}

struct ExportCategoriesResp {
    1: i32 code
    2: string msg
    3: list<CategoryTemplate> items
}

// The body is capped at 8 MB.
struct ImportCategoriesReq {
    1: i64 filesystem_id (go.tag='json:"filesystem_id,string"', api.vd="$ > 0")
    2: list<CategoryTemplate> items
}

struct ImportCategoriesResp {
    1: i32 code
    2: string msg
    3: i32 created
    // invalid, duplicate or failed items
    4: i32 skipped
}
