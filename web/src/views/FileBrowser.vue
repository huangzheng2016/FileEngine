<template>
  <!-- Empty state: no filesystems -->
  <section v-if="loaded && filesystems.length === 0" class="page-card files-empty">
    <div class="empty-state-card">
      <div class="empty-state-card__icon">
        <el-icon :size="28"><FolderDelete /></el-icon>
      </div>
      <div class="empty-state-card__text">
        <h4 class="empty-state-card__title">{{ $t('files.noFs') }}</h4>
      </div>
      <el-button type="primary" @click="$router.push('/settings/filesystems')">{{ $t('files.addFsNow') }}</el-button>
    </div>
  </section>

  <div v-else-if="loaded" class="files-page">
    <!-- Single compact toolbar: everything on one row, never wraps to a second line -->
    <header class="files-toolbar page-card">
      <el-select v-model="selectedFsId" :placeholder="$t('files.selectFs')" class="tb-fs" @change="onFsChange">
        <el-option v-for="fs in filesystems" :key="fs.id" :label="`[${fs.protocol.toUpperCase()}] ${fs.name}`" :value="fs.id">
          <span style="display: flex; align-items: center; gap: 6px">
            <span class="status-chip" :class="protocolChipClass(fs.protocol)" style="width: 52px; justify-content: center">{{ fs.protocol.toUpperCase() }}</span>
            {{ fs.name }}
          </span>
        </el-option>
      </el-select>
      <el-select v-if="sessions.length > 1" v-model="sessionId" :placeholder="$t('files.session')" class="tb-session" @change="onSessionChange">
        <el-option v-for="s in sessions" :key="s.id" :label="formatSessionLabel(s)" :value="s.id" />
      </el-select>
      <span v-else-if="selectedFsId && sessions.length === 0" class="side-hint">{{ $t('files.noSession') }}</span>
      <el-input v-model="search" :placeholder="$t('files.searchPlaceholder')" clearable class="tb-search" @clear="loadFiles" @keyup.enter="loadFiles" />
      <el-select v-model="typeFilter" :placeholder="$t('files.typeFilter')" clearable class="tb-filter" @change="loadFiles">
        <el-option :label="$t('files.file')" value="file" />
        <el-option :label="$t('files.directory')" value="directory" />
      </el-select>
      <el-select v-model="taggedFilter" :placeholder="$t('files.taggedFilter')" clearable class="tb-filter" @change="loadFiles">
        <el-option :label="$t('files.tagged')" value="true" />
        <el-option :label="$t('files.untagged')" value="false" />
      </el-select>
      <el-select v-model="categoryFilter" :placeholder="$t('files.categoryFilter')" clearable class="tb-category" @change="loadFiles">
        <el-option :label="$t('files.categorized')" value="__categorized__" />
        <el-option :label="$t('files.uncategorized')" value="__uncategorized__" />
        <el-option v-for="cat in categories" :key="cat.id" :label="cat.name" :value="cat.path" />
      </el-select>
      <div class="tb-spacer"></div>
      <el-tooltip :content="$t('files.manageCategories')" placement="bottom">
        <button class="icon-button" :aria-label="$t('files.manageCategories')" @click="catDrawerVisible = true"><el-icon><Collection /></el-icon></button>
      </el-tooltip>
      <el-popover trigger="click" width="220">
        <template #reference>
          <button class="icon-button" :title="$t('files.columns')" :aria-label="$t('files.columns')"><el-icon><Setting /></el-icon></button>
        </template>
        <draggable v-model="allColumns" item-key="key" handle=".drag-handle" @end="saveColumnPrefs">
          <template #item="{ element }">
            <div style="display: flex; align-items: center; gap: 6px; padding: 4px 0; cursor: default">
              <el-icon class="drag-handle" style="cursor: grab; color: var(--text-muted)"><Rank /></el-icon>
              <el-checkbox v-model="element.visible" @change="saveColumnPrefs" style="margin-right: 0" />
              <span style="font-size: 13px">{{ element.label }}</span>
            </div>
          </template>
        </draggable>
      </el-popover>
    </header>

    <div class="files-body">
      <!-- Unified nav tree: directories / categories in one collapsible pane -->
      <aside class="files-nav page-card" :class="{ 'is-folded': navFolded }">
        <div class="files-nav__head">
          <el-tabs v-if="!navFolded" v-model="navTab" stretch>
            <el-tab-pane :label="$t('files.navDirs')" name="dirs" />
            <el-tab-pane :label="$t('files.navCats')" name="cats" />
          </el-tabs>
          <el-tooltip :content="navFolded ? $t('nav.expand') : $t('nav.collapse')" placement="right">
            <button class="rail-toggle rail-toggle--sm" :aria-label="navFolded ? $t('nav.expand') : $t('nav.collapse')" :aria-expanded="!navFolded" @click="navFolded = !navFolded">
              <el-icon :size="14"><Fold v-if="!navFolded" /><Expand v-else /></el-icon>
            </button>
          </el-tooltip>
        </div>
        <div v-show="!navFolded" class="files-nav__scroll">
          <el-tree ref="treeRef" v-show="navTab === 'dirs'" :data="treeData" :props="{ label: 'label', children: 'children', isLeaf: 'is_leaf' }"
            lazy :load="loadNode" node-key="original_path" highlight-current @node-click="onNodeClick" />
          <div v-show="navTab === 'cats'" class="cat-tree">
            <div v-if="categories.length === 0" class="side-hint">{{ $t('files.noCategories') }}</div>
            <div v-for="cat in categories" :key="cat.id"
              class="cat-item" :class="{ 'is-active': categoryFilter === cat.path }"
              @click="setCategoryFilter(cat.path)">
              <div class="cat-item__main">
                <div class="cat-item__name">
                  <span>{{ cat.name }}</span>
                  <span v-if="cat.agent_created" class="status-chip is-accent" style="height: 18px; padding: 0 8px">AI</span>
                </div>
                <div class="cat-item__path">{{ cat.path }}</div>
              </div>
            </div>
          </div>
        </div>
      </aside>

      <!-- File table as the main zone -->
      <div class="files-main">
        <el-alert v-if="currentSession?.status === 'scanning'" type="warning" show-icon :closable="false" class="scan-banner">
          <template #title>
            <code v-if="currentSession.current_path">{{ $t('tasks.scanningPath', { path: currentSession.current_path }) }}</code>
            <span v-else>{{ $t('tasks.scanSaving') }}</span>
            · {{ $t('tasks.scannedCount') }} {{ currentSession.scanned_count }}
          </template>
        </el-alert>
        <nav class="path-nav">
          <el-tooltip :content="$t('files.goUp')" placement="bottom">
            <button class="icon-button icon-button--sm path-nav__btn" :disabled="!currentPath" :aria-label="$t('files.goUp')" @click="goUp">
              <el-icon :size="14"><Back /></el-icon>
            </button>
          </el-tooltip>

          <div v-if="!addrMode" class="path-nav__crumbs" @click.self="openAddr">
            <button class="crumb" @click="navigateTo('')">{{ $t('files.rootDir') }}</button>
            <template v-for="seg in crumbSegments" :key="seg.hidden ? 'collapsed' : seg.path">
              <span class="crumb-sep">/</span>
              <el-dropdown v-if="seg.hidden" trigger="click" @command="navigateTo">
                <button class="crumb">…</button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item v-for="h in seg.hiddenSegs" :key="h.path" :command="h.path">{{ h.label }}</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <button v-else class="crumb" :class="{ 'is-current': seg.path === currentPath }" @click="navigateTo(seg.path)">{{ seg.label }}</button>
            </template>
          </div>
          <el-input
            v-else ref="addrInputRef" v-model="addrText" class="path-nav__addr" size="small"
            :placeholder="$t('files.pathPlaceholder')"
            @keyup.enter="jumpToAddr" @keyup.esc="addrMode = false" @blur="addrMode = false" />

          <el-tooltip v-if="!addrMode" :content="$t('files.editPath')" placement="bottom">
            <button class="icon-button icon-button--sm path-nav__btn" :aria-label="$t('files.editPath')" @click="openAddr">
              <el-icon :size="14"><Position /></el-icon>
            </button>
          </el-tooltip>
          <el-tooltip :content="$t('files.copyPath')" placement="bottom">
            <button class="icon-button icon-button--sm path-nav__btn" :aria-label="$t('files.copyPath')" @click="copyCurrentPath">
              <el-icon :size="14"><CopyDocument /></el-icon>
            </button>
          </el-tooltip>
        </nav>
        <section class="page-card table-card">
          <el-table :data="files" v-loading="tableLoading" style="width: 100%" height="100%" @selection-change="onSelectionChange">
            <el-table-column type="selection" width="40" />
            <template v-for="col in visibleColumns" :key="col.key">
              <el-table-column v-if="col.key === 'name'" prop="name" :label="col.label" min-width="200">
                <template #default="{ row }">
                  <span class="name-cell" :class="{ 'name-cell--drill': row.file_type === 'directory' }" @click.stop="row.file_type === 'directory' ? drillDir(row) : openEditDrawer(row)">
                    <el-icon v-if="row.file_type === 'directory'" style="color: var(--warning)"><Folder /></el-icon>
                    <el-icon v-else style="color: var(--text-muted)"><Document /></el-icon>
                    <span class="name-cell__text">{{ row.name }}</span>
                    <el-icon class="name-cell__edit" @click.stop="openEditDrawer(row)"><Edit /></el-icon>
                  </span>
                </template>
              </el-table-column>
              <el-table-column v-else-if="col.key === 'file_type'" prop="file_type" :label="col.label" width="100">
                <template #default="{ row }">
                  <span v-if="row.file_type === 'directory'" class="status-chip is-warning">{{ $t('files.directory') }}</span>
                  <span v-else class="status-chip is-muted">{{ $t('files.file') }}</span>
                </template>
              </el-table-column>
              <el-table-column v-else-if="col.key === 'size'" :label="col.label" width="90">
                <template #default="{ row }"><span class="cell-time">{{ formatSize(row.size) }}</span></template>
              </el-table-column>
              <el-table-column v-else-if="col.key === 'description'" prop="description" :label="col.label" min-width="180" show-overflow-tooltip />
              <el-table-column v-else-if="col.key === 'tagged'" :label="col.label" width="80" align="center">
                <template #default="{ row }">
                  <span v-if="row.tagged" class="status-chip is-live">{{ $t('common.yes') }}</span>
                  <span v-else class="status-chip is-muted">{{ $t('common.no') }}</span>
                </template>
              </el-table-column>
              <el-table-column v-else-if="col.key === 'new_path'" :label="col.label" min-width="180" show-overflow-tooltip>
                <template #default="{ row}">
                  <span v-if="row.new_path" class="cell-time">{{ displayPath(row.new_path) }}</span>
                  <span v-else-if="row.file_type === 'directory'" class="cell-muted">{{ $t('files.containerDir') }}</span>
                  <span v-else-if="row.tagged" class="cell-muted">{{ $t('files.noCategoryYet') }}</span>
                  <span v-else class="cell-muted">{{ $t('files.unprocessed') }}</span>
                </template>
              </el-table-column>
              <el-table-column v-else-if="col.key === 'executed_at'" :label="col.label" width="160">
                <template #default="{ row }">
                  <template v-if="row.executed && row.executed_at">
                    <span class="status-chip" :class="row.execute_mode === 'move' ? 'is-warning' : 'is-live'">{{ row.execute_mode }}</span>
                    <span class="cell-time" style="margin-left: 6px">{{ row.executed_at?.slice(0, 16).replace('T', ' ') }}</span>
                  </template>
                </template>
              </el-table-column>
              <el-table-column v-else-if="col.key === 'preview'" :label="col.label" width="60" align="center">
                <template #default="{ row }">
                  <button v-if="row.file_type === 'file'" class="icon-button icon-button--sm" @click.stop="previewFile(row)">
                    <el-icon :size="14"><View /></el-icon>
                  </button>
                </template>
              </el-table-column>
            </template>
            <template #empty>
              <div class="empty-state-card empty-state-card--inline">
                <div class="empty-state-card__icon">
                  <el-icon :size="24"><Search /></el-icon>
                </div>
                <div class="empty-state-card__text">
                  <h4 class="empty-state-card__title">{{ $t('files.title') }}</h4>
                </div>
              </div>
            </template>
          </el-table>
        </section>

        <footer v-if="total > pageSize" class="page-pager files-pager">
          <el-pagination
            layout="total, prev, pager, next" :total="total" :page-size="pageSize"
            v-model:current-page="page" @current-change="loadFiles" background />
        </footer>

        <!-- Selection action bar -->
        <div v-if="selectedFiles.length > 0" class="selection-bar">
          <span>{{ $t('files.instructSelected', { count: selectedFiles.length }) }}</span>
          <el-button type="primary" size="small" @click="instructDialogVisible = true">
            <el-icon style="margin-right: 4px"><MagicStick /></el-icon>{{ $t('files.instruct') }}
          </el-button>
        </div>
      </div>
    </div>

    <!-- Category management drawer (moved off the main browsing area) -->
    <el-drawer append-to-body v-model="catDrawerVisible" :title="$t('files.categoryManagement')" size="420px" direction="rtl">
      <div class="cat-actions">
        <el-button size="small" @click="handleExportCategories">{{ $t('categories.exportCategories') }}</el-button>
        <el-button size="small" @click="triggerImportCategories">{{ $t('categories.importCategories') }}</el-button>
        <input ref="importFileInput" type="file" accept=".json" style="display: none" @change="handleImportCategories" />
        <el-button size="small" type="primary" @click="openCatDialog()">{{ $t('categories.addCategory') }}</el-button>
      </div>
      <div class="cat-list">
        <div v-if="categories.length === 0" class="side-hint">{{ $t('files.noCategories') }}</div>
        <div v-for="cat in categories" :key="cat.id"
          class="cat-item" :class="{ 'is-active': categoryFilter === cat.path }"
          @click="setCategoryFilter(cat.path)">
          <div class="cat-item__main">
            <div class="cat-item__name">
              <span>{{ cat.name }}</span>
              <span v-if="cat.agent_created" class="status-chip is-accent" style="height: 18px; padding: 0 8px">AI</span>
            </div>
            <div class="cat-item__path">{{ cat.path }}</div>
          </div>
          <div class="cat-item__actions">
            <button class="icon-button icon-button--sm" @click.stop="openCatDialog(cat)"><el-icon :size="14"><Edit /></el-icon></button>
            <button class="icon-button icon-button--sm is-danger" @click.stop="handleDeleteCat(cat.id)"><el-icon :size="14"><Delete /></el-icon></button>
          </div>
        </div>
      </div>
    </el-drawer>

    <!-- Category CRUD dialog -->
    <el-dialog append-to-body v-model="catDialogVisible" :title="editingCatId ? $t('categories.editCategory') : $t('categories.addCategory')" width="500px">
      <div class="form-stack">
        <div class="form-field">
          <label class="form-field__label is-required">{{ $t('common.name') }}</label>
          <el-input v-model="catForm.name" />
        </div>
        <div class="form-field">
          <label class="form-field__label is-required">{{ $t('common.path') }}</label>
          <el-input v-model="catForm.path" :placeholder="$t('categories.pathPlaceholder')" />
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('categories.structure') }}</label>
          <el-input v-model="catForm.structure" type="textarea" :rows="2" :placeholder="$t('categories.structurePlaceholder')" />
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('common.description') }}</label>
          <el-input v-model="catForm.description" type="textarea" :rows="2" :placeholder="$t('categories.descriptionPlaceholder')" />
        </div>
        <div class="form-field">
          <label class="form-field__label">{{ $t('categories.agentEditable') }}</label>
          <div><el-switch v-model="catForm.agent_editable" /></div>
          <p class="form-field__hint">{{ $t('categories.agentEditableHint') }}</p>
        </div>
      </div>
      <template #footer>
        <el-button @click="catDialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="handleSaveCat">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- File preview drawer -->
    <el-drawer append-to-body v-model="previewVisible" :title="previewFileName" size="50%" direction="rtl">
      <div v-if="previewLoading" class="drawer-state">
        <Loading visible inline :label="$t('files.previewLoading')" />
      </div>
      <div v-else-if="previewType === 'image'" style="text-align: center; padding: 16px">
        <img :src="previewUrl" style="max-width: 100%; max-height: 80vh; object-fit: contain; border-radius: var(--radius-md)" />
      </div>
      <div v-else-if="previewType === 'text'" style="padding: 0 16px">
        <pre class="preview-pre">{{ previewContent }}</pre>
      </div>
      <div v-else-if="previewType === 'too_large'" class="drawer-state">
        <el-icon :size="48" style="color: var(--warning)"><WarningFilled /></el-icon>
        <p>{{ $t('files.previewTooLarge') }}</p>
      </div>
      <div v-else class="drawer-state">
        <el-icon :size="48" style="color: var(--text-muted)"><Document /></el-icon>
        <p>{{ $t('files.previewNotSupported') }}</p>
      </div>
    </el-drawer>

    <!-- File edit drawer -->
    <el-drawer append-to-body v-model="editVisible" :title="editFile?.name" size="400px" direction="rtl">
      <template v-if="editFile">
        <el-descriptions :column="1" border size="small" style="margin-bottom: 16px">
          <el-descriptions-item :label="$t('files.originalPath')">{{ editFile.original_path }}</el-descriptions-item>
          <el-descriptions-item :label="$t('files.fileType')">
            <span v-if="editFile.file_type === 'directory'" class="status-chip is-warning">{{ $t('files.directory') }}</span>
            <span v-else class="status-chip is-muted">{{ $t('files.file') }}</span>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('files.fileSize')">{{ formatSize(editFile.size) }}</el-descriptions-item>
          <el-descriptions-item :label="$t('files.modTime')">{{ editFile.mod_time }}</el-descriptions-item>
        </el-descriptions>
        <div class="form-stack">
          <div class="form-field">
            <label class="form-field__label">{{ $t('files.fileDescription') }}</label>
            <el-input v-model="editFile.description" type="textarea" :rows="3" :placeholder="$t('files.descriptionPlaceholder')" />
          </div>
          <div class="form-field">
            <label class="form-field__label">{{ $t('files.plannedPath') }}</label>
            <el-input v-model="editFile.new_path" :placeholder="$t('files.newPathPlaceholder')" />
          </div>
          <div>
            <el-button type="primary" @click="saveEditFile">{{ $t('files.saveChanges') }}</el-button>
          </div>
        </div>
      </template>
    </el-drawer>

    <!-- Instruct dialog -->
    <el-dialog append-to-body v-model="instructDialogVisible" :title="$t('files.instructTitle')" width="600px">
      <div class="form-stack">
        <div class="instruct-files">
          <div v-for="f in selectedFiles" :key="f.id" class="cell-time">{{ f.original_path }} ({{ f.file_type }})</div>
        </div>
        <el-input v-model="instructPrompt" type="textarea" :rows="4" :placeholder="$t('files.instructPrompt')" />
      </div>
      <template #footer>
        <el-button @click="instructDialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" @click="handleInstruct">
          {{ $t('files.instruct') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { listFilesystems, listSessions, listCategories, createCategory, updateCategory, deleteCategory, exportCategories, importCategories, getFileTree, listFiles as apiListFiles, updateFile, getFileContent, instructAgent } from '../api'
import type { FileEntry, Filesystem, ScanSession, Category, TreeNode, CategoryExportItem } from '../types'
import { ElMessage, ElMessageBox, ElTree } from 'element-plus'
import draggable from 'vuedraggable'
import Loading from '../components/Loading.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

// Column configuration
interface ColumnDef { key: string; label: string; visible: boolean }
const defaultColumnOrder: { key: string; defaultVisible: boolean }[] = [
  { key: 'name', defaultVisible: true },
  { key: 'new_path', defaultVisible: true },
  { key: 'description', defaultVisible: true },
  { key: 'file_type', defaultVisible: true },
  { key: 'tagged', defaultVisible: true },
  { key: 'size', defaultVisible: false },
  { key: 'executed_at', defaultVisible: false },
  { key: 'preview', defaultVisible: false },
]
const columnLabels: Record<string, () => string> = {
  name: () => t('files.fileName'), file_type: () => t('files.fileType'), size: () => t('files.fileSize'),
  description: () => t('files.fileDescription'), tagged: () => t('files.taggedStatus'),
  new_path: () => t('files.plannedPath'), executed_at: () => t('files.executedAt'), preview: () => t('files.preview'),
}
function loadColumnPrefs(): ColumnDef[] {
  try {
    const saved = JSON.parse(localStorage.getItem('fe_columns_v2') || '[]') as { key: string; visible: boolean }[]
    if (saved.length > 0) {
      const result: ColumnDef[] = []
      for (const s of saved) {
        if (columnLabels[s.key]) {
          result.push({ key: s.key, label: columnLabels[s.key](), visible: s.visible })
        }
      }
      for (const dc of defaultColumnOrder) {
        if (!result.find(r => r.key === dc.key)) {
          result.push({ key: dc.key, label: columnLabels[dc.key](), visible: dc.defaultVisible })
        }
      }
      return result
    }
  } catch {}
  return defaultColumnOrder.map(dc => ({ key: dc.key, label: columnLabels[dc.key](), visible: dc.defaultVisible }))
}
const allColumns = ref<ColumnDef[]>(loadColumnPrefs())
const visibleColumns = computed(() => allColumns.value.filter(c => c.visible))
function saveColumnPrefs() {
  localStorage.setItem('fe_columns_v2', JSON.stringify(allColumns.value.map(c => ({ key: c.key, visible: c.visible }))))
}

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
const tableLoading = ref(false)
const search = ref('')
const typeFilter = ref('')
const taggedFilter = ref('')
const categoryFilter = ref('')
const currentPath = ref('')
const treeData = ref<TreeNode[]>([])
const catDialogVisible = ref(false)
const editingCatId = ref<number | null>(null)
const catForm = ref({ name: '', path: '', structure: '', description: '', agent_editable: false })
// Unified nav pane: directory tree / category tree in one foldable sidebar;
// category management lives in a drawer off the main browsing area.
const navTab = ref<'dirs' | 'cats'>('dirs')
const navFolded = ref(false)
const catDrawerVisible = ref(false)

// Directory rows drill into the table (same as clicking the tree node);
// files open the edit drawer instead.
function drillDir(row: FileEntry) {
  navigateTo(row.original_path)
}

// ===== Path navigation: breadcrumb / address bar / up-one-level =====
const treeRef = ref<InstanceType<typeof ElTree> | null>(null)
const addrMode = ref(false)
const addrText = ref('')
const addrInputRef = ref<any>(null)

const pathSegments = computed(() => (currentPath.value ? currentPath.value.split('/') : []))

interface CrumbSeg { label: string; path: string; hidden?: boolean; hiddenSegs?: { label: string; path: string }[] }
// Long paths collapse their middle segments into a … dropdown (first + last 4 stay).
const MAX_VISIBLE_CRUMBS = 6
const crumbSegments = computed<CrumbSeg[]>(() => {
  const segs = pathSegments.value.map((label, i) => ({ label, path: pathSegments.value.slice(0, i + 1).join('/') }))
  if (segs.length <= MAX_VISIBLE_CRUMBS) return segs
  return [segs[0], { label: '…', path: '', hidden: true, hiddenSegs: segs.slice(1, segs.length - 4) }, ...segs.slice(-4)]
})

function navigateTo(path: string) {
  if (path === currentPath.value) return
  currentPath.value = path
  page.value = 1
  loadFiles()
  syncTreeTo(path) // fire-and-forget: tree selection follows the breadcrumb
}

function goUp() {
  if (!currentPath.value) return
  const i = currentPath.value.lastIndexOf('/')
  navigateTo(i < 0 ? '' : currentPath.value.slice(0, i))
}

function expandNode(node: any): Promise<void> {
  return new Promise(resolve => {
    if (node.expanded || node.isLeaf) return resolve()
    node.expand(() => resolve())
  })
}

/** Walk the lazy directory tree level by level, expanding ancestors and
    highlighting the target. Returns false when a segment doesn't exist —
    doubles as path validation for the address bar. */
async function syncTreeTo(path: string): Promise<boolean> {
  const tree = treeRef.value
  if (!tree) return false
  if (!path) { tree.setCurrentKey(); return true }
  const segs = path.split('/')
  let prefix = ''
  for (const seg of segs) {
    prefix = prefix ? `${prefix}/${seg}` : seg
    const node = tree.getNode(prefix)
    if (!node) return false
    if (prefix !== path) await expandNode(node)
  }
  tree.setCurrentKey(path)
  return true
}

function openAddr() {
  addrText.value = currentPath.value
  addrMode.value = true
  nextTick(() => addrInputRef.value?.focus())
}

async function jumpToAddr() {
  // Tolerant cleanup: surrounding spaces and stray leading/trailing slashes.
  const cleaned = addrText.value.trim().replace(/^\/+|\/+$/g, '')
  if (!cleaned) {
    addrMode.value = false
    navigateTo('')
    return
  }
  if (!(await syncTreeTo(cleaned))) {
    ElMessage.error(t('files.pathNotFound', { path: cleaned }))
    return // keep the input open and untouched, stay where we are
  }
  addrMode.value = false
  navigateTo(cleaned)
}

async function copyCurrentPath() {
  try {
    await navigator.clipboard.writeText(currentPath.value || '/')
    ElMessage.success(t('common.copied'))
  } catch { ElMessage.error(t('common.error')) }
}

// Selection is mirrored into the URL (?fs=N&session=N) so session
// center CTAs land with the right context preselected.
onMounted(async () => {
  const res = await listFilesystems()
  filesystems.value = res.data
  loaded.value = true
  if (filesystems.value.length > 0) {
    const qFs = Number(route.query.fs)
    const lastId = Number(localStorage.getItem('fe_last_fs_id'))
    const pick = filesystems.value.find(f => f.id === qFs)
      || filesystems.value.find(f => f.id === lastId)
      || filesystems.value[0]
    selectedFsId.value = pick.id
    await onFsChange()
    const qSession = Number(route.query.session)
    if (qSession && sessions.value.find(s => s.id === qSession) && qSession !== sessionId.value) {
      sessionId.value = qSession
      await onSessionChange()
    }
  }
})

watch(() => [route.query.fs, route.query.session], async ([qFs, qSession]) => {
  const fsId = Number(qFs)
  if (fsId && fsId !== selectedFsId.value && filesystems.value.find(f => f.id === fsId)) {
    selectedFsId.value = fsId
    await onFsChange()
  }
  const sessId = Number(qSession)
  if (sessId && sessId !== sessionId.value && sessions.value.find(s => s.id === sessId)) {
    sessionId.value = sessId
    await onSessionChange()
  }
})

function syncQuery() {
  if (!selectedFsId.value || !sessionId.value) return
  router.replace({ query: { fs: String(selectedFsId.value), session: String(sessionId.value) } })
}

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
  syncScanPoll()
}

// ===== Scan progress banner =====
// The selected session's live scan state drives a banner + light poll; when
// the scan finishes the tree/table reload (rows only land in the DB at the
// end of the walk, so they were empty until then).
const currentSession = computed(() => sessions.value.find(s => s.id === sessionId.value) || null)
let scanPollTimer: ReturnType<typeof setInterval> | null = null

function syncScanPoll() {
  const scanning = currentSession.value?.status === 'scanning'
  if (scanning && !scanPollTimer) {
    scanPollTimer = setInterval(refreshSessions, 3000)
  } else if (!scanning && scanPollTimer) {
    clearInterval(scanPollTimer)
    scanPollTimer = null
  }
}

async function refreshSessions() {
  if (!selectedFsId.value) return
  const wasScanning = currentSession.value?.status === 'scanning'
  const res = await listSessions(selectedFsId.value)
  sessions.value = res.data
  if (wasScanning && currentSession.value && currentSession.value.status !== 'scanning') {
    onSessionChange()
  }
  syncScanPoll()
}

onUnmounted(() => {
  if (scanPollTimer) { clearInterval(scanPollTimer); scanPollTimer = null }
})

async function onSessionChange() {
  currentPath.value = ''
  addrMode.value = false
  addrText.value = ''
  page.value = 1
  if (!sessionId.value) { treeData.value = []; files.value = []; return }
  syncQuery()
  const res = await getFileTree(sessionId.value, '')
  treeData.value = res.data
  loadFiles()
  syncScanPoll()
}

async function loadNode(node: any, resolve: (data: TreeNode[]) => void) {
  if (node.level === 0) { resolve(treeData.value); return }
  const res = await getFileTree(sessionId.value, node.data.original_path)
  resolve(res.data)
}

function onNodeClick(data: TreeNode) {
  navigateTo(data.original_path)
}

function setCategoryFilter(path: string) {
  categoryFilter.value = categoryFilter.value === path ? '' : path
  page.value = 1
  loadFiles()
}

async function loadFiles() {
  if (!sessionId.value) return
  tableLoading.value = true
  const params: Record<string, any> = { session_id: sessionId.value, page: page.value, page_size: pageSize }
  if (currentPath.value) params.parent_path = currentPath.value
  if (search.value) params.search = search.value
  if (typeFilter.value) params.type = typeFilter.value
  if (taggedFilter.value) params.tagged = taggedFilter.value
  if (categoryFilter.value === '__categorized__') params.categorized = 'true'
  else if (categoryFilter.value === '__uncategorized__') params.categorized = 'false'
  else if (categoryFilter.value) params.category_path = categoryFilter.value
  try {
    const res = await apiListFiles(params)
    files.value = res.data.files || []
    total.value = res.data.total
  } finally {
    tableLoading.value = false
  }
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
  })
  ElMessage.success(t('common.saved'))
  editVisible.value = false
  loadFiles()
}

// Selection + Instruct
const selectedFiles = ref<FileEntry[]>([])
const instructDialogVisible = ref(false)
const instructPrompt = ref('')

function onSelectionChange(rows: FileEntry[]) {
  selectedFiles.value = rows
}

async function handleInstruct() {
  if (!sessionId.value || selectedFiles.value.length === 0 || !instructPrompt.value.trim()) return
  try {
    await instructAgent(sessionId.value, selectedFiles.value.map(f => f.id), instructPrompt.value)
    ElMessage.success(t('files.instructStarted'))
    instructDialogVisible.value = false
    instructPrompt.value = ''
    setTimeout(loadFiles, 3000)
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || 'Instruct failed')
  }
}

function openCatDialog(cat?: Category) {
  if (cat) {
    editingCatId.value = cat.id
    catForm.value = { name: cat.name, path: cat.path, structure: cat.structure, description: cat.description, agent_editable: cat.agent_editable }
  } else {
    editingCatId.value = null
    catForm.value = { name: '', path: '', structure: '', description: '', agent_editable: false }
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

// Category export/import
const importFileInput = ref<HTMLInputElement | null>(null)

async function handleExportCategories() {
  try {
    const res = await exportCategories(selectedFsId.value)
    const blob = new Blob([JSON.stringify(res.data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `categories_fs_${selectedFsId.value}.json`
    a.click()
    URL.revokeObjectURL(url)
  } catch { ElMessage.error(t('common.error')) }
}

function triggerImportCategories() {
  importFileInput.value?.click()
}

async function handleImportCategories(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    const text = await file.text()
    const items: CategoryExportItem[] = JSON.parse(text)
    const res = await importCategories(selectedFsId.value, items)
    ElMessage.success(t('categories.importSuccess', { created: res.data.created, skipped: res.data.skipped }))
    categories.value = (await listCategories(selectedFsId.value)).data
  } catch { ElMessage.error(t('categories.importFailed')) }
  finally { if (importFileInput.value) importFileInput.value.value = '' }
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0; let size = bytes
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return size.toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
}

function displayPath(path: string): string {
  if (!path || !categoryFilter.value || categoryFilter.value.startsWith('__')) return path
  const prefix = categoryFilter.value.endsWith('/') ? categoryFilter.value : categoryFilter.value + '/'
  return path.startsWith(prefix) ? path.slice(prefix.length) : path
}

function protocolChipClass(protocol: string): string {
  const map: Record<string, string> = {
    local: 'is-muted', sftp: 'is-live', ftp: 'is-accent', smb: 'is-warning', nfs: 'is-danger'
  }
  return map[protocol] || 'is-muted'
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

<style scoped>
.files-page {
  display: flex;
  flex-direction: column;
  gap: 14px;
  flex: 1;
  min-height: 0;
}

.files-empty {
  flex: 1;
  justify-content: center;
}

/* ===== Single compact toolbar (never wraps) ===== */
.files-toolbar {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  border-radius: var(--radius-lg);
  overflow-x: auto;
  flex-shrink: 0;
}

.files-toolbar > * {
  flex-shrink: 0;
}

.tb-fs { width: 172px; }
.tb-session { width: 140px; }
.tb-search { width: 160px; }
.tb-filter { width: 100px; }
.tb-category { width: 132px; }
.tb-spacer { flex: 1 1 auto; }

/* ===== Body: unified nav + main table ===== */
.files-body {
  display: flex;
  gap: 14px;
  flex: 1;
  min-height: 0;
}

.files-nav {
  width: 264px;
  flex-shrink: 0;
  padding: 12px;
  border-radius: var(--radius-lg);
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow: hidden;
  transition: width var(--duration-base) var(--ease-spring), padding var(--duration-base) var(--ease-spring);
}

.files-nav.is-folded {
  width: 44px;
  padding: 12px 6px;
}

.files-nav__head {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.files-nav__head .el-tabs {
  flex: 1;
}

/* Base styles used to come from MainLayout's scoped block, which never
   applied here (scoped CSS is per-component) — the button rendered unstyled.
   Self-contained now that MainLayout's collapse rail is removed. */
.rail-toggle {
  display: grid;
  place-items: center;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-soft);
  background: var(--input-bg);
  color: var(--text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-spring);
}

.rail-toggle:hover {
  background: var(--input-bg-hover);
  color: var(--accent);
  border-color: var(--floating-border);
}

.rail-toggle--sm {
  width: 28px;
  height: 28px;
  flex-shrink: 0;
}

.files-nav__scroll {
  flex: 1 1 0;
  min-height: 0;
  overflow: auto;
}

.cat-tree {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

/* ===== Path navigation: up / breadcrumb / address bar ===== */
.path-nav {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  min-width: 0;
}

.path-nav__btn {
  flex-shrink: 0;
}

.path-nav__btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
  transform: none;
}

.path-nav__crumbs {
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 2px;
  overflow-x: auto;
  padding: 2px 0;
  white-space: nowrap;
}

.crumb {
  display: inline-block;
  background: transparent;
  border: none;
  padding: 3px 7px;
  border-radius: var(--radius-xs);
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: background-color var(--duration-fast) var(--ease-spring),
              color var(--duration-fast) var(--ease-spring);
}

.crumb:hover {
  background: var(--accent-soft);
  color: var(--accent);
}

.crumb.is-current {
  color: var(--text-primary);
  font-weight: 600;
}

.crumb-sep {
  color: var(--text-muted);
  font-size: 12px;
  flex-shrink: 0;
}

.path-nav__addr {
  flex: 1 1 0;
  min-width: 0;
}

.cell-muted {
  color: var(--text-muted);
  font-size: 12.5px;
}

.name-cell--drill {
  color: var(--accent);
}

.cat-actions {
  display: flex;
  justify-content: flex-start;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}

.cat-actions .el-button {
  margin-left: 0;
}

.cat-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.cat-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid transparent;
  border-left: 3px solid transparent;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-spring);
}

.cat-item:hover {
  background: var(--accent-soft);
}

.cat-item.is-active {
  background: var(--accent-soft);
  border-color: var(--floating-border);
  border-left-color: var(--accent);
}

.cat-item__main {
  flex: 1;
  min-width: 0;
}

.cat-item__name {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}

.cat-item__path {
  font-size: 11px;
  font-family: var(--font-mono);
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cat-item__actions {
  flex-shrink: 0;
  display: flex;
  gap: 4px;
}

.side-hint {
  color: var(--text-muted);
  font-size: 13px;
  text-align: center;
  padding: 8px 0;
}

/* ===== Right main ===== */
.files-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-height: 0;
}

/* Live scan progress banner above the table */
.scan-banner {
  flex-shrink: 0;
  border-radius: var(--radius-md);
}

.scan-banner :deep(.el-alert__title) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.table-card {
  flex: 1 1 0;
  min-height: 0;
  padding: 16px;
  overflow: hidden;
  gap: 0;
}

.files-pager {
  border-top: none;
  padding-top: 0;
}

.name-cell {
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--accent);
  font-weight: 500;
}

.name-cell__edit {
  color: var(--text-muted);
  font-size: 12px;
  opacity: 0;
  transition: opacity var(--duration-fast) var(--ease-spring);
}

.name-cell:hover .name-cell__edit {
  opacity: 1;
}

.selection-bar {
  flex-shrink: 0;
  padding: 10px 18px;
  background: var(--accent-soft);
  border: 1px solid var(--floating-border);
  border-radius: var(--radius-pill);
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--accent);
  font-size: 13px;
  font-weight: 600;
  backdrop-filter: blur(14px);
}

.drawer-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 40px;
  color: var(--text-muted);
}

.preview-pre {
  white-space: pre-wrap;
  word-break: break-all;
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-primary);
  background: var(--surface-soft);
  border: 1px solid var(--border-soft);
  padding: 16px;
  border-radius: var(--radius-md);
  max-height: 80vh;
  overflow: auto;
}

.instruct-files {
  max-height: 150px;
  overflow: auto;
  font-size: 12px;
  background: var(--surface-soft);
  border: 1px solid var(--border-soft);
  padding: 10px 12px;
  border-radius: var(--radius-md);
}

.icon-button--sm {
  width: 28px;
  height: 28px;
  border-radius: var(--radius-xs);
}
</style>
