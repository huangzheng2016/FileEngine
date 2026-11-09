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

const COMMON_HEAD = '- 先穷尽现有分类，确无合适归属才新建；能并入绝不新建'
const COMMON_TAIL = [
  '- 同义近义必须合并，禁止一词多类',
  '- 预期少于 5 个文件就不新建，归入更宽的上级分类',
  '- 拿不准一律进 /uncategorized',
].join('\n')

const policy = (title: string, lines: string[]) =>
  [`分类创建规范（${title}）：`, COMMON_HEAD, ...lines, COMMON_TAIL].join('\n')

export const categoryPresets: CategoryPreset[] = [
  {
    id: 'backup-archive',
    label: '备份归档型',
    policy: policy('备份归档', [
      '- 备份按内容类型入类（网站/数据库/配置/虚拟机镜像）',
      '- 名称和路径都用中文（/网站备份、/数据库备份、/配置备份、/虚拟机与镜像）',
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
    policy: policy('媒体库', [
      '- 媒体按内容类型建类（照片/视频/音乐/监控录像）',
      '- 名称和路径都用中文（/照片、/视频、/音乐、/监控录像）',
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
    policy: policy('项目代码', [
      '- 源码与产物分离：项目源码归 /项目源码，构建产物与依赖产物（dist/build/node_modules 等）归 /构建产物',
      '- 名称和路径都用中文（/项目源码、/构建产物、/工具脚本）',
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
