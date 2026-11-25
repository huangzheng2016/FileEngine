// The one registry for state labels. A new status is added here, never as a map in a page.
export type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'info'

export interface Label {
  text: string
  tone: Tone
}

export type LabelMap = Record<string, Label>

// What a session is doing or waiting for, shown at the top of its page.
export const SESSION_STAGE = {
  scanning: { text: '扫描中', tone: 'accent' },
  scan_failed: { text: '扫描失败', tone: 'danger' },
  scan_interrupted: { text: '扫描中断', tone: 'warning' },
  survey: { text: '正在写方案', tone: 'accent' },
  tagging: { text: '正在整理', tone: 'accent' },
  review: { text: '正在检查', tone: 'accent' },
  chat: { text: '助手回复中', tone: 'accent' },
  executing: { text: '正在执行', tone: 'accent' },
  ready: { text: '待整理', tone: 'neutral' },
  planned: { text: '待执行', tone: 'info' },
  no_moves: { text: '无需移动', tone: 'neutral' },
  done: { text: '已完成', tone: 'success' },
} satisfies LabelMap

// What a running scan is doing, session.scan_phase.
export const SCAN_PHASE = {
  walk: { text: '遍历文件夹', tone: 'accent' },
  metadata: { text: '读取拍摄信息', tone: 'accent' },
  duplicates: { text: '查找重复文件', tone: 'accent' },
  finishing: { text: '收尾', tone: 'accent' },
} satisfies LabelMap

export const RUN_STATUS = {
  running: { text: '运行中', tone: 'accent' },
  done: { text: '完成', tone: 'success' },
  partial: { text: '部分完成', tone: 'warning' },
  error: { text: '失败', tone: 'danger' },
  stopped: { text: '已停止', tone: 'warning' },
} satisfies LabelMap

export const RUN_PHASE = {
  survey: { text: '写方案', tone: 'info' },
  tagging: { text: '整理', tone: 'accent' },
  review: { text: '检查', tone: 'info' },
  chat: { text: '对话', tone: 'neutral' },
  executing: { text: '执行', tone: 'accent' },
  revert: { text: '撤销', tone: 'warning' },
} satisfies LabelMap

export const PLAN_STATUS = {
  draft: { text: '草稿', tone: 'warning' },
  approved: { text: '已批准', tone: 'success' },
} satisfies LabelMap

export const MEMORY_SCOPE = {
  session: { text: '本次任务', tone: 'neutral' },
  filesystem: { text: '存储', tone: 'accent' },
  global: { text: '全局', tone: 'info' },
} satisfies LabelMap

export const MEMORY_KIND = {
  preference: { text: '偏好', tone: 'info' },
  convention: { text: '约定', tone: 'accent' },
  correction: { text: '修正', tone: 'warning' },
  fact: { text: '事实', tone: 'neutral' },
  note: { text: '笔记', tone: 'neutral' },
} satisfies LabelMap

export const MEMORY_SOURCE = {
  user: { text: '手动记录', tone: 'neutral' },
  agent: { text: '助手记录', tone: 'neutral' },
  correction: { text: '来自修正', tone: 'neutral' },
  reflection: { text: '复盘总结', tone: 'neutral' },
} satisfies LabelMap

export const PROTOCOL = {
  smb: { text: 'SMB', tone: 'accent' },
  sftp: { text: 'SFTP', tone: 'accent' },
  nfs: { text: 'NFS', tone: 'info' },
  local: { text: '本机文件夹', tone: 'neutral' },
  ftp: { text: 'FTP', tone: 'warning' },
} satisfies LabelMap

export const PROVIDER = {
  openai: { text: 'OpenAI 兼容', tone: 'neutral' },
  claude: { text: 'Claude', tone: 'neutral' },
  ollama: { text: 'Ollama', tone: 'neutral' },
} satisfies LabelMap

export const EXEC_MODE = {
  copy: { text: '复制', tone: 'neutral' },
  move: { text: '移动', tone: 'neutral' },
} satisfies LabelMap

export const EXEC_MODE_HINT = {
  copy: '在新位置放一份副本，原文件不动，通常会多占一份空间',
  move: '把文件挪到新位置，原来的位置不再保留',
} as const satisfies Record<keyof typeof EXEC_MODE, string>

export const FILTER_MODE = {
  blacklist: { text: '跳过这些文件夹', tone: 'neutral' },
  whitelist: { text: '只扫描这些文件夹', tone: 'neutral' },
} satisfies LabelMap

// Decision of an entry; '' (undecided) is the key `pending`.
export const ENTRY_ROLE = {
  unit: { text: '整体移动', tone: 'accent' },
  file: { text: '移动', tone: 'success' },
  member: { text: '随文件夹移动', tone: 'info' },
  container: { text: '拆开整理', tone: 'neutral' },
  skip: { text: '保持原位', tone: 'neutral' },
  duplicate: { text: '重复', tone: 'warning' },
  pending: { text: '未整理', tone: 'warning' },
} satisfies LabelMap

export const RULE_ROLE = {
  file: { text: '逐个文件', tone: 'neutral' },
  unit: { text: '整个文件夹', tone: 'neutral' },
} satisfies LabelMap

export const VALIDATION_ISSUE = {
  invalid_target: { text: '目标位置无效', tone: 'danger' },
  into_self: { text: '目标位于源文件夹内', tone: 'danger' },
  nested: { text: '上级文件夹已有决定', tone: 'neutral' },
  collision: { text: '目标位置冲突', tone: 'danger' },
  target_exists: { text: '目标已存在', tone: 'danger' },
  source_changed: { text: '源文件已变化', tone: 'warning' },
  source_missing: { text: '源文件不存在', tone: 'warning' },
  failed: { text: '操作失败', tone: 'danger' },
} satisfies LabelMap

// Journal state of the operation that carried an entry (idl LocateItem.op_state); '' = no operation yet.
export const OP_STATE = {
  pending: { text: '进行中', tone: 'accent' },
  verified: { text: '已校验', tone: 'info' },
  done: { text: '已完成', tone: 'success' },
  failed: { text: '失败', tone: 'danger' },
  reverted: { text: '已撤销', tone: 'warning' },
} satisfies LabelMap

export const DATE_SOURCE = {
  exif: { text: '拍摄时间', tone: 'neutral' },
  video: { text: '视频创建时间', tone: 'neutral' },
  filename: { text: '文件名中的日期', tone: 'neutral' },
  mtime: { text: '修改时间', tone: 'neutral' },
} satisfies LabelMap

export const ENTRY_FLAG = {
  needs_review: { text: '需要确认', tone: 'warning' },
  sensitive: { text: '敏感文件', tone: 'danger' },
} satisfies LabelMap

export const REASONING_EFFORT = {
  low: { text: '低', tone: 'neutral' },
  medium: { text: '中', tone: 'neutral' },
  high: { text: '高', tone: 'neutral' },
  max: { text: '最高', tone: 'neutral' },
} satisfies LabelMap

// The agent is called 助手 everywhere in the UI, never AI or 智能体.
// Agent tool names as the person reads them in a transcript.
export const TOOL_LABEL: Record<string, string> = {
  list_entries: '查看文件',
  read_file: '读取文件',
  view_image: '查看图片',
  decide: '做出决定',
  split_unit: '拆分整体',
  describe: '写文件说明',
  add_rule: '新建规则',
  apply_rules: '应用规则',
  run_script: '运行脚本',
  create_category: '新建分类',
  note: '记笔记',
  recall: '查记忆',
  update_category: '修改分类',
  delete_category: '删除分类',
  update_rule: '修改规则',
  delete_rule: '删除规则',
  read_plan: '读方案',
  write_plan: '写方案',
  remember: '记住',
}

// JSON keys of the agent settings as the settings history reports them in `changed`.
export const AGENT_SETTING_FIELDS: Record<string, string> = {
  concurrency: '同时整理的文件夹数',
  max_step: '单个文件夹最多步数',
  chat_max_step: '助手每次回复最多步数',
  max_retries: '失败重试',
  rate_limit_max_retries: '限流重试',
  retry_base_delay: '重试起始间隔',
  retry_max_delay: '重试最长间隔',
  agent_timeout: '单个文件夹时限',
  max_file_read_size: '每次读取字数',
  archive_entry_max_size: '压缩包内文件读取上限',
  archive_prompt_disabled: '关闭压缩包提示',
  archive_prompt_max_entries: '压缩包提示的条目数',
  junk_filter_disabled: '关闭垃圾文件过滤',
  vision_max_images_per_batch: '每次查看的图片数',
  vision_image_max_px: '图片缩略边长',
  vision_max_image_bytes: '可查看的图片大小上限',
  script_max_size: '脚本大小上限',
  script_timeout: '脚本运行时限',
  script_max_result_size: '脚本输出大小上限',
  category_policy: '分类规范',
  system_prompt: '提示词',
}
