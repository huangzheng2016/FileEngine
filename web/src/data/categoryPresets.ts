export interface PresetCategory {
  name: string
  path: string
  description: string
}

export interface CategoryPreset {
  id: string
  label: string
  policy: string
  categories: PresetCategory[]
}

const COMMON_HEAD = [
  '- 命名：分类用 2–6 个字的中文名词，路径与名称一致；不用英文、拼音、日期、"其他""杂项"作分类名。',
  '- 新建与复用：先用现有分类，近义的并入；现有分类都不合适、且至少有 10 个文件或一个完整文件夹要放时才新建。',
]
const COMMON_TAIL = [
  '- 跳过：系统和程序生成的文件（缓存、临时文件、Thumbs.db、.DS_Store、@eaDir、回收站）保持原位，不整理。',
  '- 兜底：看不出用途的放 /未分类，并交给用户确认。',
]

const policy = (lines: string[]) => [...COMMON_HEAD, ...lines, ...COMMON_TAIL].join('\n')

export const categoryPresets: CategoryPreset[] = [
  {
    id: 'backup-archive',
    label: '备份归档型',
    policy: policy([
      '- 备份按内容分类：/网站备份、/数据库备份、/配置备份、/虚拟机与镜像。',
      '- 同一来源的备份放在一起，按来源再分一级：/网站备份/blog/、/数据库备份/mysql-main/；一次完整备份的文件夹整体移动，不拆分。',
      '- 带日期的备份文件保留原名，不按日期另建目录：/数据库备份/mysql-main/main-2024-03-01.sql.gz。',
      '- 安装包按软件名归入 /安装包/软件名/，各版本放在一起。',
    ]),
    categories: [
      { name: '网站备份', path: '/网站备份', description: '网站源码与数据的备份归档' },
      { name: '数据库备份', path: '/数据库备份', description: '数据库导出与转储文件' },
      { name: '配置备份', path: '/配置备份', description: '服务与系统的配置文件备份' },
      { name: '虚拟机与镜像', path: '/虚拟机与镜像', description: '虚拟机磁盘与系统镜像文件' },
      { name: '安装包', path: '/安装包', description: '软件安装包与发行文件' },
      { name: '文档资料', path: '/文档资料', description: '文档与参考资料' },
    ],
  },
  {
    id: 'media-library',
    label: '媒体库型',
    policy: policy([
      '- 照片、视频按拍摄年份分目录，单年很多时再按月：/照片/2023/IMG_0012.jpg、/视频/2021/08/VID_0031.mp4。',
      '- 相册、活动文件夹整体放进对应年份：/照片/2023/云南旅行/。',
      '- 音乐按 艺人/专辑 存放，专辑文件夹整体移动：/音乐/周杰伦/范特西/。',
      '- 监控录像按摄像头再按日期：/监控录像/前门/2024-05/。',
    ]),
    categories: [
      { name: '照片', path: '/照片', description: '照片与图片文件' },
      { name: '视频', path: '/视频', description: '视频文件' },
      { name: '音乐', path: '/音乐', description: '音乐与音频文件' },
      { name: '监控录像', path: '/监控录像', description: '安防监控录像文件' },
      { name: '文档', path: '/文档', description: '相关文档资料' },
    ],
  },
  {
    id: 'project-code',
    label: '项目代码型',
    policy: policy([
      '- 含 .git、package.json、go.mod 等标记的文件夹是一个项目，整体移动不拆分：/项目源码/blog-site/。',
      '- 项目外单独存在的构建产物（dist、build、发布包）放 /构建产物/项目名/。项目内部的 node_modules、dist 跟随项目，不单独整理。',
      '- 零散脚本放 /工具脚本，按用途再分一级：/工具脚本/备份/。',
      '- 不属于某个项目的说明文档放 /项目文档，数据文件放 /数据文件。',
    ]),
    categories: [
      { name: '项目源码', path: '/项目源码', description: '代码项目源码仓库' },
      { name: '构建产物', path: '/构建产物', description: '编译构建与依赖产物' },
      { name: '项目文档', path: '/项目文档', description: '项目文档' },
      { name: '工具脚本', path: '/工具脚本', description: '实用工具与脚本' },
      { name: '数据文件', path: '/数据文件', description: '项目数据文件' },
    ],
  },
]
