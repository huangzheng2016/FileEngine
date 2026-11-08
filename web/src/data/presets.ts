import type { RuleInput } from '@/api/rules'
import type { PresetCategory } from './categoryPresets'

export type ScopeKey = 'all' | 'image' | 'video' | 'audio' | 'doc' | 'archive' | 'installer' | 'custom'

export const SCOPE_EXTS: Record<Exclude<ScopeKey, 'all' | 'custom'>, string[]> = {
  image: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'heic', 'heif', 'bmp', 'tif', 'tiff', 'raw', 'cr2', 'nef', 'arw', 'dng'],
  video: ['mp4', 'mov', 'mkv', 'avi', 'wmv', 'flv', 'm4v', 'ts', 'webm', 'mts', '3gp'],
  audio: ['mp3', 'flac', 'wav', 'aac', 'm4a', 'ogg', 'ape', 'wma', 'opus'],
  doc: [
    'pdf',
    'doc',
    'docx',
    'xls',
    'xlsx',
    'ppt',
    'pptx',
    'txt',
    'md',
    'csv',
    'odt',
    'ods',
    'odp',
    'rtf',
    'epub',
    'pages',
    'numbers',
    'key',
  ],
  archive: ['zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'bz2', 'xz'],
  installer: ['exe', 'msi', 'dmg', 'pkg', 'deb', 'rpm', 'apk', 'appimage', 'iso'],
}

export const SCOPE_OPTIONS: { value: ScopeKey; label: string }[] = [
  { value: 'all', label: '全部文件' },
  { value: 'image', label: '图片' },
  { value: 'video', label: '视频' },
  { value: 'audio', label: '音频' },
  { value: 'doc', label: '文档' },
  { value: 'archive', label: '压缩包' },
  { value: 'installer', label: '安装包' },
  { value: 'custom', label: '自定义扩展名' },
]

export interface Scope {
  key: ScopeKey
  // only for key 'custom'
  exts: string[]
}

export function scopeExts(s: Scope): string[] {
  if (s.key === 'all') return []
  if (s.key === 'custom') return s.exts
  return SCOPE_EXTS[s.key]
}

export interface TaskPreset {
  id: string
  name: string
  goal: string
  scope: Scope
  rules: RuleInput[]
  categories: PresetCategory[]
}

const rule = (r: Partial<RuleInput> & Pick<RuleInput, 'name' | 'target'>): RuleInput => ({
  exts: '',
  name_regex: '',
  path_prefix: '',
  file_type: 'file',
  min_size: 0,
  max_size: 0,
  role: 'file',
  source: 'preset',
  ...r,
})

const exts = (k: keyof typeof SCOPE_EXTS) => SCOPE_EXTS[k].join(',')

export const TASK_PRESETS: TaskPreset[] = [
  {
    id: 'media-by-month',
    name: '照片视频按年月',
    goal: '照片和视频按拍摄年份、月份归档，相册和活动文件夹保持完整。',
    scope: { key: 'custom', exts: [...SCOPE_EXTS.image, ...SCOPE_EXTS.video] },
    rules: [
      rule({ name: '照片按年月', exts: exts('image'), target: '照片/{year}/{month}/{name}' }),
      rule({ name: '视频按年月', exts: exts('video'), target: '视频/{year}/{month}/{name}' }),
    ],
    categories: [
      { name: '照片', path: '/照片', description: '照片与图片，按年月归档' },
      { name: '视频', path: '/视频', description: '自拍与拍摄的视频，按年月归档' },
    ],
  },
  {
    id: 'docs-by-type',
    name: '文档按类型',
    goal: '文档按用途分类（合同、发票、报告、学习资料等），同一项目的文件放在一起。',
    scope: { key: 'doc', exts: [] },
    rules: [],
    categories: [
      { name: '合同协议', path: '/文档/合同协议', description: '合同、协议、授权书' },
      { name: '票据报销', path: '/文档/票据报销', description: '发票、收据、报销单' },
      { name: '报告方案', path: '/文档/报告方案', description: '报告、方案、总结' },
      { name: '学习资料', path: '/文档/学习资料', description: '书籍、教程、课件' },
      { name: '表格数据', path: '/文档/表格数据', description: '表格与数据文件' },
    ],
  },
  {
    id: 'downloads-cleanup',
    name: '下载目录清理',
    goal: '把下载目录里的文件按类型归位，安装包和压缩包单独放，项目文件夹保持完整。',
    scope: { key: 'all', exts: [] },
    rules: [
      rule({ name: '安装包', exts: exts('installer'), target: '安装包/{name}' }),
      rule({ name: '压缩包', exts: exts('archive'), target: '压缩包/{name}' }),
      rule({ name: '图片', exts: exts('image'), target: '图片/{year}/{name}' }),
      rule({ name: '视频', exts: exts('video'), target: '视频/{name}' }),
      rule({ name: '音频', exts: exts('audio'), target: '音频/{name}' }),
    ],
    categories: [
      { name: '安装包', path: '/安装包', description: '软件安装程序与镜像' },
      { name: '压缩包', path: '/压缩包', description: '下载的压缩文件' },
      { name: '图片', path: '/图片', description: '图片与截图' },
      { name: '视频', path: '/视频', description: '视频文件' },
      { name: '音频', path: '/音频', description: '音频文件' },
      { name: '文档', path: '/文档', description: '文档与资料' },
    ],
  },
  {
    id: 'software-by-version',
    name: '软件安装包按名称和版本',
    goal: '安装包按软件名称归类，同一软件的不同版本放在各自的版本目录里。',
    scope: { key: 'custom', exts: [...SCOPE_EXTS.installer, ...SCOPE_EXTS.archive] },
    rules: [],
    categories: [{ name: '软件', path: '/软件', description: '按 软件/名称/版本 存放的安装包' }],
  },
  {
    id: 'code-projects',
    name: '代码项目归档',
    goal: '代码项目整体归档，不拆散项目内部文件；构建产物和依赖目录跳过。',
    scope: { key: 'all', exts: [] },
    rules: [],
    categories: [
      { name: '代码项目', path: '/代码项目', description: '完整的代码仓库与工程' },
      { name: '脚本工具', path: '/脚本工具', description: '零散脚本与小工具' },
    ],
  },
  {
    id: 'tv-series',
    name: '影视剧集',
    goal: '电影按片名归档，剧集按剧名和季整理，字幕跟随对应视频。',
    scope: { key: 'custom', exts: [...SCOPE_EXTS.video, 'srt', 'ass', 'ssa', 'sub', 'nfo'] },
    rules: [],
    categories: [
      { name: '电影', path: '/影视/电影', description: '电影，每部一个文件夹' },
      { name: '剧集', path: '/影视/剧集', description: '电视剧与番剧，按 剧名/季 存放' },
    ],
  },
  {
    id: 'music-by-artist',
    name: '音乐按艺人专辑',
    goal: '音乐按艺人和专辑整理，专辑文件夹保持完整。',
    scope: { key: 'custom', exts: [...SCOPE_EXTS.audio, 'cue', 'lrc', 'jpg', 'png'] },
    rules: [],
    categories: [{ name: '音乐', path: '/音乐', description: '按 艺人/专辑 存放的音乐' }],
  },
]
