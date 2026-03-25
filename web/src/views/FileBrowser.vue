<template>
  <div style="display: flex; gap: 16px; height: calc(100vh - 100px)">
    <!-- Empty state: no filesystems -->
    <div v-if="loaded && filesystems.length === 0" style="flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; color: #909399">
      <el-icon :size="64" style="margin-bottom: 16px"><FolderDelete /></el-icon>
      <p style="font-size: 16px; margin-bottom: 12px">{{ $t('files.noFs') }}</p>
      <el-button type="primary" @click="$router.push('/filesystems')">{{ $t('files.addFsNow') }}</el-button>
    </div>

    <template v-else-if="loaded">
    <!-- Left sidebar -->
    <div style="width: 350px; flex-shrink: 0; display: flex; flex-direction: column; gap: 12px; overflow: auto">
      <!-- Filesystem selector -->
      <el-card>
        <el-select v-model="selectedFsId" :placeholder="$t('files.selectFs')" style="width: 100%" @change="onFsChange">
          <el-option v-for="fs in filesystems" :key="fs.id" :label="`[${fs.protocol.toUpperCase()}] ${fs.name}`" :value="fs.id">
            <span style="display: flex; align-items: center; gap: 6px">
              <el-tag :type="protocolTagType(fs.protocol)" size="small" effect="dark" style="width: 48px; text-align: center">{{ fs.protocol.toUpperCase() }}</el-tag>
              {{ fs.name }}
            </span>
          </el-option>
        </el-select>
        <el-select v-if="sessions.length > 1" v-model="sessionId" :placeholder="$t('files.session')" style="width: 100%; margin-top: 8px" @change="onSessionChange">
          <el-option v-for="s in sessions" :key="s.id"
            :label="formatSessionLabel(s)"
            :value="s.id" />
        </el-select>
        <div v-else-if="selectedFsId && sessions.length === 0" style="color: #909399; font-size: 13px; text-align: center; padding: 8px 0; margin-top: 8px">
          {{ $t('files.noSession') }}
        </div>
      </el-card>

      <!-- Tabs: Category / Directory tree -->
      <el-card v-if="selectedFsId" style="flex: 1; overflow: auto; display: flex; flex-direction: column">
        <el-tabs v-model="sidebarTab" style="flex: 1; display: flex; flex-direction: column">
          <el-tab-pane :label="$t('files.categoryManagement')" name="categories">
            <div style="display: flex; justify-content: flex-end; margin-bottom: 8px">
              <el-button size="small" type="primary" @click="openCatDialog()">{{ $t('categories.addCategory') }}</el-button>
            </div>
            <div v-if="categories.length === 0" style="color: #999; font-size: 13px; text-align: center; padding: 8px 0">
              {{ $t('files.noCategories') }}
            </div>
            <div v-for="cat in categories" :key="cat.id"
              style="display: flex; align-items: center; justify-content: space-between; padding: 6px 0; border-bottom: 1px solid #f0f0f0; cursor: pointer"
              @click="setCategoryFilter(cat.path)">
              <div style="flex: 1; min-width: 0">
                <div style="font-size: 13px; font-weight: 500">{{ cat.name }}</div>
                <div style="font-size: 11px; color: #999; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ cat.path }}</div>
              </div>
              <div style="flex-shrink: 0; margin-left: 8px">
                <el-button size="small" text @click.stop="openCatDialog(cat)"><el-icon><Edit /></el-icon></el-button>
                <el-button size="small" text type="danger" @click.stop="handleDeleteCat(cat.id)"><el-icon><Delete /></el-icon></el-button>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="$t('files.directoryTree')" name="tree" :disabled="!sessionId">
            <el-tree :data="treeData" :props="{ label: 'label', children: 'children', isLeaf: 'is_leaf' }"
              lazy :load="loadNode" node-key="original_path" highlight-current @node-click="onNodeClick" />
          </el-tab-pane>
        </el-tabs>
      </el-card>
    </div>

    <!-- Right main area -->
    <div style="flex: 1; display: flex; flex-direction: column; gap: 12px; overflow: hidden">
      <!-- Filter bar -->
      <el-card>
        <div style="display: flex; gap: 12px; align-items: center; flex-wrap: wrap">
          <el-input v-model="search" :placeholder="$t('files.searchPlaceholder')" clearable style="width: 180px" size="small" @clear="loadFiles" @keyup.enter="loadFiles" />
          <el-select v-model="typeFilter" :placeholder="$t('files.typeFilter')" clearable size="small" style="width: 110px" @change="loadFiles">
            <el-option :label="$t('files.file')" value="file" />
            <el-option :label="$t('files.directory')" value="directory" />
          </el-select>
          <el-select v-model="taggedFilter" :placeholder="$t('files.taggedFilter')" clearable size="small" style="width: 110px" @change="loadFiles">
            <el-option :label="$t('files.tagged')" value="true" />
            <el-option :label="$t('files.untagged')" value="false" />
          </el-select>
          <el-select v-model="categoryFilter" :placeholder="$t('files.categoryFilter')" clearable size="small" style="width: 150px" @change="loadFiles">
            <el-option :label="$t('files.categorized')" value="__categorized__" />
            <el-option :label="$t('files.uncategorized')" value="__uncategorized__" />
            <el-option v-for="cat in categories" :key="cat.id" :label="cat.name" :value="cat.path" />
          </el-select>
          <el-tag v-if="currentPath" closable @close="currentPath = ''; loadFiles()">{{ currentPath }}</el-tag>
        </div>
      </el-card>

      <!-- File table -->
      <el-card style="flex: 1; overflow: auto">
        <el-table :data="files" style="width: 100%" height="100%">
          <el-table-column prop="name" :label="$t('files.fileName')" min-width="200">
            <template #default="{ row }">
              <span style="cursor: pointer; display: inline-flex; align-items: center; gap: 4px" @click.stop="openEditDrawer(row)">
                <el-icon v-if="row.file_type === 'directory'" style="color: #e6a23c"><Folder /></el-icon>
                <el-icon v-else style="color: #909399"><Document /></el-icon>
                <span style="color: #409eff">{{ row.name }}</span>
                <el-icon style="color: #c0c4cc; font-size: 12px"><Edit /></el-icon>
              </span>
            </template>
          </el-table-column>
          <el-table-column prop="file_type" :label="$t('files.fileType')" width="90">
            <template #default="{ row }">
              <el-tag v-if="row.file_type === 'directory'" type="warning" size="small" effect="plain">{{ $t('files.directory') }}</el-tag>
              <el-tag v-else size="small" effect="plain">{{ $t('files.file') }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="$t('files.fileSize')" width="90">
            <template #default="{ row }">{{ formatSize(row.size) }}</template>
          </el-table-column>
          <el-table-column prop="description" :label="$t('files.fileDescription')" min-width="180" show-overflow-tooltip />
          <el-table-column :label="$t('files.taggedStatus')" width="70" align="center">
            <template #default="{ row }">
              <el-tag :type="row.tagged ? 'success' : 'info'" size="small">{{ row.tagged ? $t('common.yes') : $t('common.no') }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="new_path" :label="$t('files.plannedPath')" min-width="180" show-overflow-tooltip />
          <el-table-column :label="$t('files.preview')" width="60" align="center">
            <template #default="{ row }">
              <el-button v-if="row.file_type === 'file'" size="small" text @click.stop="previewFile(row)">
                <el-icon><View /></el-icon>
              </el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination v-if="total > pageSize" style="margin-top: 12px; justify-content: center"
          layout="total, prev, pager, next" :total="total" :page-size="pageSize"
          v-model:current-page="page" @current-change="loadFiles" />
      </el-card>
    </div>

    <!-- Category CRUD dialog -->
    </template>

    <el-dialog v-model="catDialogVisible" :title="editingCatId ? $t('categories.editCategory') : $t('categories.addCategory')" width="500px">
      <el-form :model="catForm" label-width="100px">
        <el-form-item :label="$t('common.name')">
          <el-input v-model="catForm.name" />
        </el-form-item>
        <el-form-item :label="$t('common.path')">
          <el-input v-model="catForm.path" :placeholder="$t('categories.pathPlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('categories.structure')">
          <el-input v-model="catForm.structure" type="textarea" :rows="2" :placeholder="$t('categories.structurePlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('common.description')">
          <el-input v-model="catForm.description" type="textarea" :rows="2" :placeholder="$t('categories.descriptionPlaceholder')" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="catDialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="handleSaveCat">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- File preview drawer -->
    <el-drawer v-model="previewVisible" :title="previewFileName" size="50%" direction="rtl">
      <div v-if="previewLoading" style="text-align: center; padding: 40px; color: #909399">
        <el-icon class="is-loading" :size="32"><Loading /></el-icon>
        <p style="margin-top: 12px">{{ $t('files.previewLoading') }}</p>
      </div>
      <div v-else-if="previewType === 'image'" style="text-align: center; padding: 16px">
        <img :src="previewUrl" style="max-width: 100%; max-height: 80vh; object-fit: contain" />
      </div>
      <div v-else-if="previewType === 'text'" style="padding: 0 16px">
        <pre style="white-space: pre-wrap; word-break: break-all; font-size: 13px; line-height: 1.6; background: #f5f7fa; padding: 16px; border-radius: 4px; max-height: 80vh; overflow: auto">{{ previewContent }}</pre>
      </div>
      <div v-else-if="previewType === 'too_large'" style="text-align: center; padding: 40px; color: #909399">
        <el-icon :size="48"><WarningFilled /></el-icon>
        <p style="margin-top: 12px">{{ $t('files.previewTooLarge') }}</p>
      </div>
      <div v-else style="text-align: center; padding: 40px; color: #909399">
        <el-icon :size="48"><Document /></el-icon>
        <p style="margin-top: 12px">{{ $t('files.previewNotSupported') }}</p>
      </div>
    </el-drawer>

    <!-- File edit drawer -->
    <el-drawer v-model="editVisible" :title="editFile?.name" size="400px" direction="rtl">
      <template v-if="editFile">
        <el-descriptions :column="1" border size="small" style="margin-bottom: 16px">
          <el-descriptions-item :label="$t('files.originalPath')">{{ editFile.original_path }}</el-descriptions-item>
          <el-descriptions-item :label="$t('files.fileType')">
            <el-tag v-if="editFile.file_type === 'directory'" type="warning" size="small" effect="plain">{{ $t('files.directory') }}</el-tag>
            <el-tag v-else size="small" effect="plain">{{ $t('files.file') }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('files.fileSize')">{{ formatSize(editFile.size) }}</el-descriptions-item>
          <el-descriptions-item :label="$t('files.modTime')">{{ editFile.mod_time }}</el-descriptions-item>
        </el-descriptions>
        <el-form label-position="top">
          <el-form-item :label="$t('files.fileDescription')">
            <el-input v-model="editFile.description" type="textarea" :rows="3" :placeholder="$t('files.descriptionPlaceholder')" />
          </el-form-item>
          <el-form-item :label="$t('files.plannedPath')">
            <el-input v-model="editFile.new_path" :placeholder="$t('files.newPathPlaceholder')" />
          </el-form-item>
          <el-form-item :label="$t('files.versionPlaceholder')">
            <el-input v-model="editFile.version" :placeholder="$t('files.versionPlaceholder')" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" @click="saveEditFile">{{ $t('files.saveChanges') }}</el-button>
          </el-form-item>
        </el-form>
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { listFilesystems, listSessions, listCategories, createCategory, updateCategory, deleteCategory, getFileTree, listFiles as apiListFiles, updateFile, getFileContent } from '../api'
import type { FileEntry, Filesystem, ScanSession, Category, TreeNode } from '../types'
import { ElMessage, ElMessageBox } from 'element-plus'

const { t } = useI18n()

const loaded = ref(false)
const filesystems = ref<Filesystem[]>([])
const selectedFsId = ref<number>(0)
const sessions = ref<ScanSession[]>([])
const sessionId = ref<number>(0)
const categories = ref<Category[]>([])

const files = ref<FileEntry[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const search = ref('')
const typeFilter = ref('')
const taggedFilter = ref('')
const categoryFilter = ref('')
const currentPath = ref('')
const treeData = ref<TreeNode[]>([])
const sidebarTab = ref('categories')
const catDialogVisible = ref(false)
const editingCatId = ref<number | null>(null)
const catForm = ref({ name: '', path: '', structure: '', description: '' })

onMounted(async () => {
  const res = await listFilesystems()
  filesystems.value = res.data
  loaded.value = true
  if (filesystems.value.length > 0) {
    const lastId = Number(localStorage.getItem('fe_last_fs_id'))
    const exists = filesystems.value.find(f => f.id === lastId)
    selectedFsId.value = exists ? lastId : filesystems.value[0].id
    onFsChange()
  }
})

async function onFsChange() {
  sessionId.value = 0
  treeData.value = []
  files.value = []
  categoryFilter.value = ''
  if (!selectedFsId.value) { sessions.value = []; categories.value = []; return }
  localStorage.setItem('fe_last_fs_id', String(selectedFsId.value))
  const [sessRes, catRes] = await Promise.all([
    listSessions(selectedFsId.value),
    listCategories(selectedFsId.value),
  ])
  sessions.value = sessRes.data
  categories.value = catRes.data
  if (sessions.value.length > 0) {
    sessionId.value = sessions.value[0].id
    onSessionChange()
  }
}

async function onSessionChange() {
  currentPath.value = ''
  page.value = 1
  if (!sessionId.value) { treeData.value = []; files.value = []; return }
  const res = await getFileTree(sessionId.value, '')
  treeData.value = res.data
  loadFiles()
}

async function loadNode(node: any, resolve: (data: TreeNode[]) => void) {
  if (node.level === 0) { resolve(treeData.value); return }
  const res = await getFileTree(sessionId.value, node.data.original_path)
  resolve(res.data)
}

function onNodeClick(data: TreeNode) {
  currentPath.value = data.original_path
  page.value = 1
  loadFiles()
}

function setCategoryFilter(path: string) {
  categoryFilter.value = categoryFilter.value === path ? '' : path
  page.value = 1
  loadFiles()
}

async function loadFiles() {
  if (!sessionId.value) return
  const params: Record<string, any> = { session_id: sessionId.value, page: page.value, page_size: pageSize }
  if (currentPath.value) params.parent_path = currentPath.value
  if (search.value) params.search = search.value
  if (typeFilter.value) params.type = typeFilter.value
  if (taggedFilter.value) params.tagged = taggedFilter.value
  if (categoryFilter.value === '__categorized__') params.categorized = 'true'
  else if (categoryFilter.value === '__uncategorized__') params.categorized = 'false'
  else if (categoryFilter.value) params.category_path = categoryFilter.value
  const res = await apiListFiles(params)
  files.value = res.data.files || []
  total.value = res.data.total
}

// Edit drawer
const editVisible = ref(false)
const editFile = ref<FileEntry | null>(null)

function openEditDrawer(file: FileEntry) {
  editFile.value = { ...file }
  editVisible.value = true
}

async function saveEditFile() {
  if (!editFile.value) return
  await updateFile(editFile.value.id, {
    description: editFile.value.description,
    new_path: editFile.value.new_path,
    version: editFile.value.version,
  })
  ElMessage.success(t('common.saved'))
  editVisible.value = false
  loadFiles()
}

function openCatDialog(cat?: Category) {
  if (cat) {
    editingCatId.value = cat.id
    catForm.value = { name: cat.name, path: cat.path, structure: cat.structure, description: cat.description }
  } else {
    editingCatId.value = null
    catForm.value = { name: '', path: '', structure: '', description: '' }
  }
  catDialogVisible.value = true
}

async function handleSaveCat() {
  const data = { ...catForm.value, filesystem_id: selectedFsId.value }
  if (editingCatId.value) await updateCategory(editingCatId.value, data)
  else await createCategory(data)
  ElMessage.success(t('common.saved'))
  catDialogVisible.value = false
  categories.value = (await listCategories(selectedFsId.value)).data
}

async function handleDeleteCat(id: number) {
  await ElMessageBox.confirm(t('categories.deleteConfirm'), t('common.confirm'))
  await deleteCategory(id)
  ElMessage.success(t('common.deleted'))
  categories.value = (await listCategories(selectedFsId.value)).data
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0; let size = bytes
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return size.toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
}

function protocolTagType(protocol: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  const map: Record<string, '' | 'success' | 'warning' | 'info' | 'danger'> = {
    local: 'info', sftp: 'success', ftp: '', smb: 'warning', nfs: 'danger'
  }
  return map[protocol] || 'info'
}

function formatSessionLabel(s: ScanSession): string {
  const date = new Date(s.created_at).toLocaleDateString()
  const path = s.scan_path || t('tasks.allDirectories')
  return `${path} · ${s.total_files} ${t('files.file')} · ${date}`
}

// File preview
const previewVisible = ref(false)
const previewLoading = ref(false)
const previewFileName = ref('')
const previewContent = ref('')
const previewUrl = ref('')
const previewType = ref<'text' | 'image' | 'too_large' | 'unsupported'>('unsupported')

const textExts = new Set(['.txt', '.md', '.json', '.yaml', '.yml', '.xml', '.csv', '.log', '.ini', '.conf', '.toml', '.sh', '.bat', '.py', '.js', '.ts', '.go', '.java', '.c', '.cpp', '.h', '.html', '.css', '.sql', '.env', '.gitignore', '.dockerfile'])
const imageExts = new Set(['.jpg', '.jpeg', '.png', '.gif', '.bmp', '.webp', '.svg', '.ico'])
const MAX_PREVIEW_SIZE = 1024 * 1024 // 1MB

function getExt(name: string): string {
  const i = name.lastIndexOf('.')
  return i >= 0 ? name.slice(i).toLowerCase() : ''
}

async function previewFile(file: FileEntry) {
  previewFileName.value = file.name
  previewContent.value = ''
  if (previewUrl.value) { URL.revokeObjectURL(previewUrl.value); previewUrl.value = '' }

  const ext = getExt(file.name)
  if (file.size > MAX_PREVIEW_SIZE && !imageExts.has(ext)) {
    previewType.value = 'too_large'
    previewVisible.value = true
    return
  }
  if (!textExts.has(ext) && !imageExts.has(ext)) {
    previewType.value = 'unsupported'
    previewVisible.value = true
    return
  }

  previewType.value = imageExts.has(ext) ? 'image' : 'text'
  previewLoading.value = true
  previewVisible.value = true

  try {
    const res = await getFileContent(file.id)
    const blob = res.data as Blob
    if (previewType.value === 'image') {
      previewUrl.value = URL.createObjectURL(blob)
    } else {
      previewContent.value = await blob.text()
    }
  } catch (e: any) {
    previewContent.value = 'Error: ' + (e.response?.data?.error || e.message)
    previewType.value = 'text'
  } finally {
    previewLoading.value = false
  }
}
</script>
