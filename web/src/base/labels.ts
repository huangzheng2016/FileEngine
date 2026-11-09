// The one registry for state labels. A new status is added here, never as a map in a page.
export type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'info'

export interface Label {
  text: string
  tone: Tone
}

export type LabelMap = Record<string, Label>

export const SESSION_STATUS = {
  scanning: { text: '扫描中', tone: 'accent' },
  scanned: { text: '扫描完成', tone: 'neutral' },
  error: { text: '扫描失败', tone: 'danger' },
  interrupted: { text: '扫描中断', tone: 'warning' },
} satisfies LabelMap

// What a session is doing or waiting for, shown at the top of its page.
export const SESSION_STAGE = {
  scanning: { text: '扫描中', tone: 'accent' },
  scan_failed: { text: '扫描失败', tone: 'danger' },
  scan_interrupted: { text: '扫描中断', tone: 'warning' },
  survey: { text: '正在生成方案', tone: 'accent' },
  tagging: { text: '正在整理', tone: 'accent' },
  review: { text: '正在复查', tone: 'accent' },
  chat: { text: '助手回复中', tone: 'accent' },
  executing: { text: '正在执行', tone: 'accent' },
  ready: { text: '待整理', tone: 'neutral' },
  planned: { text: '待执行', tone: 'info' },
  no_moves: { text: '无需移动', tone: 'neutral' },
  done: { text: '已完成', tone: 'success' },
} satisfies LabelMap

export const RUN_STATUS = {
  running: { text: '运行中', tone: 'accent' },
  done: { text: '完成', tone: 'success' },
  partial: { text: '部分完成', tone: 'warning' },
  error: { text: '失败', tone: 'danger' },
  stopped: { text: '已停止', tone: 'warning' },
} satisfies LabelMap

export const RUN_PHASE = {
  survey: { text: '生成方案', tone: 'info' },
  tagging: { text: '整理', tone: 'accent' },
  review: { text: '复查', tone: 'info' },
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
  local: { text: '本机目录', tone: 'neutral' },
  ftp: { text: 'FTP', tone: 'warning' },
} satisfies LabelMap

export const PROVIDER = {
  openai: { text: 'OpenAI 兼容', tone: 'neutral' },
  claude: { text: 'Claude', tone: 'neutral' },
  ollama: { text: 'Ollama', tone: 'neutral' },
} satisfies LabelMap

export const PREVIEW_STATUS = {
  moved: { text: '将移动', tone: 'accent' },
  partial: { text: '部分移动', tone: 'info' },
  kept: { text: '不动', tone: 'neutral' },
} satisfies LabelMap

export const FILE_TYPE = {
  directory: { text: '目录', tone: 'neutral' },
  file: { text: '文件', tone: 'neutral' },
  symlink: { text: '链接', tone: 'neutral' },
} satisfies LabelMap

export const EXEC_MODE = {
  copy: { text: '复制', tone: 'neutral' },
  move: { text: '移动', tone: 'neutral' },
} satisfies LabelMap

export const FILTER_MODE = {
  blacklist: { text: '跳过这些目录', tone: 'neutral' },
  whitelist: { text: '只扫描这些目录', tone: 'neutral' },
} satisfies LabelMap

// Decision of an entry; '' (undecided) is the key `pending`.
export const ENTRY_ROLE = {
  unit: { text: '整体移动', tone: 'accent' },
  file: { text: '单独移动', tone: 'success' },
  member: { text: '随目录', tone: 'info' },
  container: { text: '逐项处理', tone: 'neutral' },
  skip: { text: '跳过', tone: 'neutral' },
  duplicate: { text: '重复', tone: 'warning' },
  pending: { text: '未整理', tone: 'warning' },
} satisfies LabelMap

export const RULE_ROLE = {
  file: { text: '逐个文件', tone: 'neutral' },
  unit: { text: '整个目录', tone: 'neutral' },
} satisfies LabelMap

export const VALIDATION_ISSUE = {
  invalid_target: { text: '目标位置无效', tone: 'danger' },
  into_self: { text: '目标位置在自身内部', tone: 'danger' },
  nested: { text: '随上级目录移动', tone: 'neutral' },
  collision: { text: '目标位置重名', tone: 'danger' },
  target_exists: { text: '目标位置已有文件', tone: 'danger' },
  source_changed: { text: '源文件已变化', tone: 'warning' },
  source_missing: { text: '源文件不存在', tone: 'warning' },
  failed: { text: '执行失败', tone: 'danger' },
} satisfies LabelMap

export const DATE_SOURCE = {
  exif: { text: '拍摄时间', tone: 'neutral' },
  video: { text: '视频创建时间', tone: 'neutral' },
  filename: { text: '文件名中的日期', tone: 'neutral' },
  mtime: { text: '修改时间', tone: 'neutral' },
} satisfies LabelMap

export const ENTRY_FLAG = {
  needs_review: { text: '待确认', tone: 'warning' },
  sensitive: { text: '敏感文件', tone: 'danger' },
} satisfies LabelMap

export const REASONING_EFFORT = {
  low: { text: '低', tone: 'neutral' },
  medium: { text: '中', tone: 'neutral' },
  high: { text: '高', tone: 'neutral' },
  max: { text: '最高', tone: 'neutral' },
} satisfies LabelMap

// Agent tool names as the person reads them in a transcript.
export const TOOL_LABEL: Record<string, string> = {
  list_files: '查看文件',
  get_file_info: '查看文件信息',
  read_file: '读取文件',
  view_image: '查看图片',
  run_script: '运行脚本',
  set_targets: '设定目标位置',
  decide_dirs: '决定目录',
  split_unit: '拆分整体',
  update_description: '写文件说明',
  list_categories: '查看分类',
  create_category: '新建分类',
  update_category: '修改分类',
  delete_category: '删除分类',
  add_rule: '新建规则',
  update_rule: '修改规则',
  delete_rule: '删除规则',
  apply_rules: '应用规则',
  read_plan: '读方案',
  write_plan: '写方案',
  note: '记笔记',
  remember: '记住',
  recall: '查记忆',
}
