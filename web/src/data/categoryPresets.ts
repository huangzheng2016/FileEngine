// Built-in category templates with a matching category-creation policy text
// per template. Names/descriptions/policy are bilingual; the UI picks the
// current locale. Importing a template seeds only missing categories via the
// existing category import endpoint (duplicates are skipped server-side).

export interface PresetCategory {
  name: { 'zh-CN': string; en: string }
  path: string
  description: { 'zh-CN': string; en: string }
}

export interface CategoryPreset {
  id: string
  label: { 'zh-CN': string; en: string }
  policy: { 'zh-CN': string; en: string }
  categories: PresetCategory[]
}

export const categoryPresets: CategoryPreset[] = [
  {
    id: 'backup-archive',
    label: { 'zh-CN': '备份归档型', en: 'Backup Archive' },
    policy: {
      'zh-CN': `分类创建规范（备份归档）：
- 先穷尽现有分类，确无合适归属才新建；能并入绝不新建
- 备份按内容类型入类（网站/数据库/配置/虚拟机镜像），绝不按时间、设备名或人名建类
- 名称用语义化中文（网站备份/数据库备份/配置备份/虚拟机与镜像），路径用小写英文短横线（/website-backups）
- 同义近义必须合并，禁止一词多类
- 预期少于 5 个文件就不新建，归入更宽的上级分类
- 拿不准一律进 /uncategorized`,
      en: `Category creation policy (backup archive):
- Exhaust existing categories first; create a new one only when nothing fits — merge whenever possible
- File backups by content type (website/database/config/VM images), never by date, device name, or person
- Use semantic Chinese names (网站备份/数据库备份/配置备份/虚拟机与镜像) with lowercase hyphenated English paths (/website-backups)
- Merge synonyms and near-duplicates; never allow multiple categories for one concept
- Do not create a category expecting fewer than 5 files; file them under a broader parent instead
- When in doubt, file under /uncategorized`,
    },
    categories: [
      {
        name: { 'zh-CN': '网站备份', en: 'Website Backups' },
        path: '/website-backups',
        description: { 'zh-CN': '网站源码与数据的备份归档', en: 'Website source and data backup archives' },
      },
      {
        name: { 'zh-CN': '数据库备份', en: 'Database Backups' },
        path: '/database-backups',
        description: { 'zh-CN': '数据库导出与转储文件', en: 'Database exports and dump files' },
      },
      {
        name: { 'zh-CN': '配置备份', en: 'Config Backups' },
        path: '/config-backups',
        description: { 'zh-CN': '服务与系统的配置文件备份', en: 'Configuration backups for services and systems' },
      },
      {
        name: { 'zh-CN': '虚拟机与镜像', en: 'VMs & Images' },
        path: '/vm-images',
        description: { 'zh-CN': '虚拟机磁盘与系统镜像文件', en: 'Virtual machine disks and system images' },
      },
      {
        name: { 'zh-CN': '安装包', en: 'Packages' },
        path: '/packages',
        description: { 'zh-CN': '软件安装包与发行文件', en: 'Software installers and distribution packages' },
      },
      {
        name: { 'zh-CN': '文档资料', en: 'Documents' },
        path: '/documents',
        description: { 'zh-CN': '文档与参考资料', en: 'Documents and reference materials' },
      },
    ],
  },
  {
    id: 'media-library',
    label: { 'zh-CN': '媒体库型', en: 'Media Library' },
    policy: {
      'zh-CN': `分类创建规范（媒体库）：
- 先穷尽现有分类，确无合适归属才新建；能并入绝不新建
- 媒体按内容类型建类（照片/视频/音乐/监控录像），绝不按日期、事件或设备名建类
- 名称用语义化中文（照片/视频/音乐/监控录像），路径用小写英文短横线（/photos）
- 同义近义必须合并，禁止一词多类
- 预期少于 5 个文件就不新建，归入更宽的上级分类
- 拿不准一律进 /uncategorized`,
      en: `Category creation policy (media library):
- Exhaust existing categories first; create a new one only when nothing fits — merge whenever possible
- File media by content type (photos/videos/music/surveillance), never by date, event, or device name
- Use semantic Chinese names (照片/视频/音乐/监控录像) with lowercase hyphenated English paths (/photos)
- Merge synonyms and near-duplicates; never allow multiple categories for one concept
- Do not create a category expecting fewer than 5 files; file them under a broader parent instead
- When in doubt, file under /uncategorized`,
    },
    categories: [
      {
        name: { 'zh-CN': '照片', en: 'Photos' },
        path: '/photos',
        description: { 'zh-CN': '照片与图片文件', en: 'Photos and image files' },
      },
      {
        name: { 'zh-CN': '视频', en: 'Videos' },
        path: '/videos',
        description: { 'zh-CN': '视频文件', en: 'Video files' },
      },
      {
        name: { 'zh-CN': '音乐', en: 'Music' },
        path: '/music',
        description: { 'zh-CN': '音乐与音频文件', en: 'Music and audio files' },
      },
      {
        name: { 'zh-CN': '监控录像', en: 'Surveillance' },
        path: '/surveillance',
        description: { 'zh-CN': '安防监控录像文件', en: 'Surveillance camera recordings' },
      },
      {
        name: { 'zh-CN': '文档', en: 'Documents' },
        path: '/documents',
        description: { 'zh-CN': '相关文档资料', en: 'Related documents' },
      },
    ],
  },
  {
    id: 'project-code',
    label: { 'zh-CN': '项目代码型', en: 'Project Code' },
    policy: {
      'zh-CN': `分类创建规范（项目代码）：
- 先穷尽现有分类，确无合适归属才新建；能并入绝不新建
- 源码与产物分离：项目源码归 /source-code，构建产物与依赖产物（dist/build/node_modules 等）归 /build-artifacts
- 按"内容类型"建分类，不按人名/时间/设备名建
- 名称用语义化中文（项目源码/构建产物/工具脚本），路径用小写英文短横线（/source-code）
- 同义近义必须合并，禁止一词多类
- 预期少于 5 个文件就不新建，归入更宽的上级分类
- 拿不准一律进 /uncategorized`,
      en: `Category creation policy (project code):
- Exhaust existing categories first; create a new one only when nothing fits — merge whenever possible
- Separate source from artifacts: project source goes to /source-code; build and dependency artifacts (dist/build/node_modules etc.) go to /build-artifacts
- Create categories by content type, never by person, date, or device name
- Use semantic Chinese names (项目源码/构建产物/工具脚本) with lowercase hyphenated English paths (/source-code)
- Merge synonyms and near-duplicates; never allow multiple categories for one concept
- Do not create a category expecting fewer than 5 files; file them under a broader parent instead
- When in doubt, file under /uncategorized`,
    },
    categories: [
      {
        name: { 'zh-CN': '项目源码', en: 'Source Code' },
        path: '/source-code',
        description: { 'zh-CN': '代码项目源码仓库', en: 'Source code repositories' },
      },
      {
        name: { 'zh-CN': '构建产物', en: 'Build Artifacts' },
        path: '/build-artifacts',
        description: { 'zh-CN': '编译构建与依赖产物', en: 'Build outputs and dependency artifacts' },
      },
      {
        name: { 'zh-CN': '文档', en: 'Docs' },
        path: '/docs',
        description: { 'zh-CN': '项目文档', en: 'Project documentation' },
      },
      {
        name: { 'zh-CN': '工具脚本', en: 'Scripts' },
        path: '/scripts',
        description: { 'zh-CN': '实用工具与脚本', en: 'Utility tools and scripts' },
      },
      {
        name: { 'zh-CN': '数据文件', en: 'Data Files' },
        path: '/data',
        description: { 'zh-CN': '项目数据文件', en: 'Project data files' },
      },
    ],
  },
]
