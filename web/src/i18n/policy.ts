// Category-policy UI strings live in their own module (not locales/*.yaml)
// so the feature stays self-contained; index.ts spread-merges these under
// the top-level `categoryPolicy` namespace, which no yaml locale defines.
const policy = {
  'zh-CN': {
    categoryPolicy: {
      title: '分类规范',
      hint: '规范文本会注入 system prompt 的「分类规范」章节，并附加到 create_category 工具描述末尾；保存后对新发起的打标任务生效；清空保存将恢复默认规范',
      quickFill: '快速填充',
      quickFillPlaceholder: '选择预设模板填入规范文本（填入后可再修改）',
      importTitle: '从模板导入分类',
      selectTemplate: '选择模板',
      selectFilesystem: '目标文件系统',
      importButton: '导入缺失分类',
      importResult: '导入完成：新建 {created} 个，跳过 {skipped} 个（已存在或无效）',
      importFailed: '导入失败',
    },
  },
  en: {
    categoryPolicy: {
      title: 'Category Policy',
      hint: 'The policy text is injected into the system prompt as a "Category Policy" section and appended to the create_category tool description; it applies to tagging runs started after saving. Clearing it and saving restores the default policy',
      quickFill: 'Quick fill',
      quickFillPlaceholder: 'Pick a preset to fill the policy text (editable afterwards)',
      importTitle: 'Import Categories from Template',
      selectTemplate: 'Template',
      selectFilesystem: 'Target filesystem',
      importButton: 'Import missing categories',
      importResult: 'Import done: {created} created, {skipped} skipped (existing or invalid)',
      importFailed: 'Import failed',
    },
  },
}

export default policy
