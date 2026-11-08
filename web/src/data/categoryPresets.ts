// Category templates: a policy text for the agent and the categories it starts from.

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

export const categoryPresets: CategoryPreset[] = [
  {
    id: 'backup-archive',
    label: '备份归档型',
    policy:
      '分类创建规范（备份归档）：\n- 先穷尽现有分类，确无合适归属才新建；能并入绝不新建\n- 备份按内容类型入类（网站/数据库/配置/虚拟机镜像）\n- 名称用语义化中文（网站备份/数据库备份/配置备份/虚拟机与镜像），路径用小写英文短横线（/website-backups）\n- 同义近义必须合并，禁止一词多类\n- 预期少于 5 个文件就不新建，归入更宽的上级分类\n- 拿不准一律进 /uncategorized',
    categories: [
      {
        name: '网站备份',
        path: '/website-backups',
        description: '网站源码与数据的备份归档',
      },
      {
        name: '数据库备份',
        path: '/database-backups',
        description: '数据库导出与转储文件',
      },
      {
        name: '配置备份',
        path: '/config-backups',
        description: '服务与系统的配置文件备份',
      },
      {
        name: '虚拟机与镜像',
        path: '/vm-images',
        description: '虚拟机磁盘与系统镜像文件',
      },
      {
        name: '安装包',
        path: '/packages',
        description: '软件安装包与发行文件',
      },
      {
        name: '文档资料',
        path: '/documents',
        description: '文档与参考资料',
      },
    ],
  },
  {
    id: 'media-library',
    label: '媒体库型',
    policy:
      '分类创建规范（媒体库）：\n- 先穷尽现有分类，确无合适归属才新建；能并入绝不新建\n- 媒体按内容类型建类（照片/视频/音乐/监控录像）\n- 名称用语义化中文（照片/视频/音乐/监控录像），路径用小写英文短横线（/photos）\n- 同义近义必须合并，禁止一词多类\n- 预期少于 5 个文件就不新建，归入更宽的上级分类\n- 拿不准一律进 /uncategorized',
    categories: [
      {
        name: '照片',
        path: '/photos',
        description: '照片与图片文件',
      },
      {
        name: '视频',
        path: '/videos',
        description: '视频文件',
      },
      {
        name: '音乐',
        path: '/music',
        description: '音乐与音频文件',
      },
      {
        name: '监控录像',
        path: '/surveillance',
        description: '安防监控录像文件',
      },
      {
        name: '文档',
        path: '/docs',
        description: '相关文档资料',
      },
    ],
  },
  {
    id: 'project-code',
    label: '项目代码型',
    policy:
      '分类创建规范（项目代码）：\n- 先穷尽现有分类，确无合适归属才新建；能并入绝不新建\n- 源码与产物分离：项目源码归 /source-code，构建产物与依赖产物（dist/build/node_modules 等）归 /build-artifacts\n- 名称用语义化中文（项目源码/构建产物/工具脚本），路径用小写英文短横线（/source-code）\n- 同义近义必须合并，禁止一词多类\n- 预期少于 5 个文件就不新建，归入更宽的上级分类\n- 拿不准一律进 /uncategorized',
    categories: [
      {
        name: '项目源码',
        path: '/source-code',
        description: '代码项目源码仓库',
      },
      {
        name: '构建产物',
        path: '/build-artifacts',
        description: '编译构建与依赖产物',
      },
      {
        name: '项目文档',
        path: '/project-docs',
        description: '项目文档',
      },
      {
        name: '工具脚本',
        path: '/scripts',
        description: '实用工具与脚本',
      },
      {
        name: '数据文件',
        path: '/data',
        description: '项目数据文件',
      },
    ],
  },
]
