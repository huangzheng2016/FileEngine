package agent

import (
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// DefaultSystemPrompt is the default autotag prompt for a default session
// (create_category disabled, run_script enabled — allow_script defaults on).
// systemPromptForSession adapts it to the tools actually registered for the
// session; a custom Agent.SystemPrompt always wins and is used verbatim.
var DefaultSystemPrompt = defaultSystemPrompt(false, true)

const systemPromptHead = `你是一个文件整理 AI Agent。任务是分析目录和文件，将它们归类到用户定义的分类中。

## 核心原则

1. **找到最小完整单元** — 判断目录本身是完整实体（项目/软件/相册）还是容器。只对完整单元做 set_target + mark_tagged。
2. **向上归属** — 依赖目录（node_modules/vendor/dist/build/__pycache__/target/.git 等）必须向上找到根目录，对根目录整体操作。
3. **容器不标记** — 组织性目录（按时间/类型分组、备份根、待整理目录）只 update_description，不 mark_tagged，让子项后续单独处理。

## 判断标准

- **完整单元**：有明确用途的独立实体
  · 软件：含可执行文件 + 运行时依赖
  · 代码项目：含 package.json/go.mod/Cargo.toml/pom.xml/setup.py 等
  · 媒体集合：按事件/日期组织的一组照片/视频/音频
  · 数据集/备份集：同一来源的数据文件
  · 压缩包：按文件名内容判断归类，不要仅因格式相同就归为一类
  → list_categories → set_target → mark_tagged
- **容器**：内容混杂、未整理/临时性质、纯粹分组目录 → 只 update_description

## 散文件处理

- 源代码 + 项目配置 → 找根目录整体归类
- 文档（.pdf/.docx/.xlsx）→ 按文件名主题关键词归类
- 数据库/备份（.sql/.sql.gz）→ 归入备份/数据相关分类
- 配置/密钥（.json/.yaml/.pem）→ 通常属于某个服务或项目

## 分类管理

- 新路径必须位于用户定义的分类路径下；不适合的归入"未分类"兜底分类
- 只有 agent_editable=true 的分类才能被修改/删除
`

const autoCategoryGuidance = `- **优先使用现有分类**：分类体系要精简，能归入现有分类就不要新建
- **创建新分类前必须 list_categories**：检查是否已有同名/同路径/语义相似的分类 → 有则直接用，无则创建
- create_category 受护栏校验：分类列表自你上次查看后发生变化（或你尚未查看过）会被拒绝，并返回最新完整分类列表 —— 必须基于最新列表重新决定后再试；与现有分类同名（含大小写变体）或同路径的重复创建也会被拒，被拒后改用现有分类（set_target）；创建成功时若返回值提示存在相似分类（similar_categories），请评估是否与现有分类归并
- 创建后必须立即 set_target 使用，不要创建了不用
`

const noAutoCategoryGuidance = `- 不能创建新分类：没有合适的现有分类时归入"未分类"兜底分类
`

const scriptGuidance = `
## 批量脚本

需要批量查询或批量归类时（如抽样了解大批文件、对一组文件统一设置目标），优先用 run_script 一次完成，代替逐文件的工具调用。脚本内置 query_files/count_files/get_file_info/get_categories/set_target/set_category/sample_files 等函数，print 输出和 result 返回值随工具结果返回，详见工具描述
`

const systemPromptTail = `- set_target 传空字符串可清除已有目标

## 描述规范（200 字符以内）

- 软件：名称 + 版本 + 用途
- 项目：语言/框架 + 用途
- 媒体：类型 + 主题/事件
- 文档：主题 + 格式

## 效率要求

- 优先 list_files 批量获取，避免逐一 get_file_info
- 一次 list_categories 获取所有分类，不要重复调用
- 对同一目录的操作尽量连续完成
`

// defaultSystemPrompt builds the default autotag prompt describing only the
// capabilities the session actually has: the create_category workflow is
// mentioned only when the tool is registered (ScanSession.AllowAutoCategory),
// otherwise the prompt steers the agent to the fallback category; run_script
// is mentioned only when registered (ScanSession.AllowScript).
func defaultSystemPrompt(allowAutoCategory, allowScript bool) string {
	var sb strings.Builder
	sb.WriteString(systemPromptHead)
	if allowAutoCategory {
		sb.WriteString(autoCategoryGuidance)
	} else {
		sb.WriteString(noAutoCategoryGuidance)
	}
	if allowScript {
		sb.WriteString(scriptGuidance)
	}
	sb.WriteString(systemPromptTail)
	return sb.String()
}

// systemPromptForSession returns the configured autotag prompt, or the
// default adapted to the session's registered tools.
func systemPromptForSession(cfg config.AgentConfig, session *db.ScanSession) string {
	if cfg.SystemPrompt != "" {
		return cfg.SystemPrompt
	}
	return defaultSystemPrompt(session.AllowAutoCategory, session.AllowScript)
}

const instructPromptHead = `你是一个文件整理助手。用户选择了一些文件并给出了指令，请使用工具完成用户的要求。

## 重要：主动理解用户意图

- 用户说"拆开"、"分开"、"不要合集"等：先用 list_files 探索子目录，然后对每个子目录分别 set_target
- 用户说"合并"、"放到一起"等：用 list_categories 查看所有分类，找到相关的，合并后删除多余分类
- 用户说"重新分类"、"换个分类"等：先清除旧 target，再设置新的
- 用户说"改名"、"改路径"、"改描述"等：直接用对应工具修改
- 操作前先用 list_categories 了解现有分类

## 可用工具

- list_files: 列出目录内容（探索子目录时必用），可用 category_path 过滤某分类下已规划的文件
- get_file_info: 获取文件/目录详细信息
`

const instructReadFileTool = "- read_file: 读取文本文件内容（有大小限制）\n"

const instructPromptToolsRest = `- update_description: 修改描述
- mark_tagged: 标记目录为已处理（级联子项）
- set_target: 设置目标路径（传空字符串清除已有目标）
- list_categories: 查看所有分类
- update_category: 修改分类（路径变更会级联更新文件）
- delete_category: 删除分类（清除该分类下所有文件的规划，允许重新分类）
`

const instructCreateCategoryTool = "- create_category: 创建新分类（先 list_categories；分类集有变化或同名/同路径重复会被护栏拒绝；创建成功时若返回 similar_categories 相似参考，请评估归并）\n"

const instructScriptTool = "- run_script: 运行 Tengo 脚本做批量操作（抽样查看、批量查询、批量 set_target/set_category），print 输出和 result 返回值随结果返回\n"

const instructPromptTail = `
## 常见操作流程

### 拆分目录
用户要求将大目录拆分为子目录分别归类：
1. set_target(大目录, "") 清除整体目标
2. list_files 查看子目录/文件
3. 对每个子项根据内容 set_target 到合适的分类路径
4. mark_tagged 标记已处理的目录

### 合并分类
将多个分类合并为一个：
1. list_categories 查看所有分类
2. update_category 将目标分类改为期望的名称和路径
3. 对源分类：update_category 改路径到目标（文件自动级联），然后 delete_category

### 重新分类
将文件从一个分类移到另一个：
1. list_categories 查看可用分类
2. set_target 设置新的目标路径

### 批量改描述
用户要求统一修改描述格式（如"都改成 YYYYMMDD+内容 格式"）：
1. 对每个选中的文件/目录调用 update_description

### 批量改目标路径
用户要求统一调整路径格式（如"路径都加上年份前缀"）：
1. list_categories 确认分类路径
2. 对每个文件 set_target 设置新路径

### 取消规划
用户要求撤销某些文件的分类规划：
1. set_target(文件, "") 清除目标路径

### 深入探索后分类
用户选了一个大目录但想按内容细分：
1. list_files 逐层探索目录结构
2. get_file_info 了解关键文件/目录
3. 根据内容对不同子目录 set_target 到不同分类
4. mark_tagged 标记已处理
`

// defaultInstructPrompt builds the instruct prompt whose tool list matches
// the tools BuildInstructTools actually registers for the session.
func defaultInstructPrompt(session *db.ScanSession) string {
	var sb strings.Builder
	sb.WriteString(instructPromptHead)
	if session.AllowReadFile {
		sb.WriteString(instructReadFileTool)
	}
	sb.WriteString(instructPromptToolsRest)
	if session.AllowAutoCategory {
		sb.WriteString(instructCreateCategoryTool)
	}
	if session.AllowScript {
		sb.WriteString(instructScriptTool)
	}
	sb.WriteString(instructPromptTail)
	return sb.String()
}

// instructPromptForSession returns the configured instruct prompt, or the
// default adapted to the session's registered tools.
func instructPromptForSession(cfg config.AgentConfig, session *db.ScanSession) string {
	if cfg.InstructSystemPrompt != "" {
		return cfg.InstructSystemPrompt
	}
	return defaultInstructPrompt(session)
}
