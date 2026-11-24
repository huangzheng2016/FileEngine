<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import Pagination from '@/base/Pagination.vue'
import Select from '@/base/Select.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtBytes, fmtLabel, fmtNumber } from '@/base/format'
import { DATE_SOURCE, ENTRY_FLAG, ENTRY_ROLE, EXEC_MODE, OP_STATE } from '@/base/labels'
import { fmtDate } from '@/base/time'
import { toast } from '@/base/toast'
import { useConfirm } from '@/base/useConfirm'
import { useFetch } from '@/base/useFetch'
import { useList } from '@/base/useList'
import { useSelection } from '@/base/useSelection'
import { listCategories, listFiles, resetPlans } from '@/api/files'
import { locateFile } from '@/api/sessions'
import type { FileEntry, LocateItem, Session } from '@/api/types'
import DirTree from '@/components/DirTree.vue'
import { addChatFiles } from '@/lib/chatFiles'
import { pathCrumbs } from '@/lib/paths'
import { landing, shownPath } from '@/lib/toolView'
import CategoryDialog from './CategoryDialog.vue'
import FilePreviewDialog from './FilePreviewDialog.vue'
import TargetDialog from './TargetDialog.vue'

const props = defineProps<{ session: Session }>()
const emit = defineEmits<{ changed: [] }>()
const confirm = useConfirm()

// The folder, search and category live in the URL beside the page, so a reload or a shared link lands on the
// same listing. Searching looks through every folder.
const list = useList(
  (p, signal) =>
    listFiles(
      {
        session_id: props.session.id,
        parent_path: p.dir,
        search: p.fq,
        category_path: p.cat,
        page: p.page,
        size: p.size,
      },
      signal,
    ),
  { dir: '', fq: '', cat: '' },
  { search: 'fq' },
)
watch(
  () => props.session.id,
  () => void list.refresh(),
)

const rows = list.items
const filtered = computed(() => !!(list.filters.fq || list.filters.cat))

const categories = useFetch((signal) => listCategories(props.session.filesystem_id, signal), { immediate: true })
const categoryOptions = computed(() => [
  { value: '', label: '全部分类' },
  ...(categories.data.value ?? []).map((c) => ({ value: c.path, label: c.name })),
])

const dir = computed({
  get: () => list.filters.dir,
  set: (d: string) => {
    list.filters.dir = d
    list.filters.fq = ''
  },
})

function clearFilters() {
  list.filters.fq = ''
  list.filters.cat = ''
}

const selection = useSelection(computed(() => rows.value.map((r) => ({ id: r.id }))))
const selectedRows = computed(() => rows.value.filter((r) => selection.has(r.id)))

function roleKey(f: FileEntry) {
  return f.role || 'pending'
}

// A member's reason is its folder's; the role pill already says it moves with the folder.
const detailLine = (f: FileEntry) => (f.role === 'container' || f.role === 'member' ? '' : f.reason)

// mtime is a fallback every file has; only real capture dates are worth a line.
const showDate = (f: FileEntry) => !!f.taken_at && !!f.date_source && f.date_source !== 'mtime'

const trail = computed(() => pathCrumbs(list.filters.dir))

function open(f: FileEntry) {
  if (f.file_type === 'directory') dir.value = f.original_path
  else previewing.value = f
}

const previewing = ref<FileEntry | null>(null)
const categoriesOpen = ref(false)
const editing = ref<FileEntry | null>(null)

function askAssistant(rowsToAsk: FileEntry[]) {
  addChatFiles(
    props.session.id,
    rowsToAsk.map((f) => ({ id: f.id, name: f.name })),
  )
  toast.success(`已把 ${fmtNumber(rowsToAsk.length)} 项加入对话`)
  selection.clear()
}

const resetting = ref(false)
async function reset(targets: FileEntry[]) {
  if (resetting.value) return
  const decided = targets.filter((f) => f.role).length
  if (!decided) {
    toast.error('重新整理失败：所选项目还没有整理')
    return
  }
  const ok = await confirm({
    message:
      targets.length === 1
        ? `重新整理「${targets[0].name}」？它现有的去向会被清除。`
        : `重新整理所选 ${fmtNumber(targets.length)} 项？它们现有的去向会被清除。`,
    confirmText: '重新整理',
    danger: true,
  })
  if (!ok) return
  resetting.value = true
  try {
    await resetPlans(
      props.session.id,
      targets.map((f) => f.id),
    )
    toast.success(targets.length === 1 ? '已交给助手重新整理' : `已把 ${fmtNumber(targets.length)} 项交给助手重新整理`)
    selection.clear()
    void list.refresh()
    emit('changed')
  } catch (e) {
    toast.error('重新整理失败', e)
  } finally {
    resetting.value = false
  }
}

function resetBlocked(f: FileEntry): string {
  if (!f.role) return '还没有整理'
  return resetting.value ? '正在保存' : ''
}

const menuFor = (f: FileEntry) => [
  { label: '预览', icon: 'eye' as const, onSelect: () => (previewing.value = f) },
  { label: '问助手', icon: 'message-square' as const, onSelect: () => askAssistant([f]) },
  {
    label: '重新整理',
    icon: 'rotate-ccw' as const,
    disabled: !!resetBlocked(f),
    title: resetBlocked(f) || undefined,
    onSelect: () => void reset([f]),
  },
]

const locate = reactive({
  open: false,
  q: '',
  asked: '',
  loading: false,
  done: false,
  items: [] as LocateItem[],
})
async function runLocate() {
  const q = locate.q.trim()
  if (!q || locate.loading) return
  locate.loading = true
  try {
    locate.items = await locateFile(props.session.id, q)
    locate.asked = q
    locate.done = true
  } catch (e) {
    toast.error('查找失败', e)
  } finally {
    locate.loading = false
  }
}
function openLocate() {
  Object.assign(locate, { open: true, q: '', asked: '', done: false, items: [] })
}

function locateLine(r: LocateItem): string {
  if (!r.new_path) return '保持原位'
  const to = shownPath(r.new_path)
  const via = r.decided_by && r.decided_by !== r.original_path ? `（跟着「${shownPath(r.decided_by)}」一起）` : ''
  return r.executed ? `已${fmtLabel(EXEC_MODE, r.execute_mode)}到 ${to}${via}` : `新位置 ${to}${via}`
}
</script>

<template>
  <div class="files">
    <aside class="card files-tree">
      <DirTree v-model="dir" :session-id="session.id" />
    </aside>
    <section class="files-main">
      <nav class="files-crumbs text-sm" aria-label="位置">
        <template v-for="(c, i) in trail" :key="c.path">
          <Icon v-if="i" name="chevron-right" :size="12" class="text-muted" />
          <button
            type="button"
            class="ghost btn-sm"
            :aria-current="i === trail.length - 1 || undefined"
            @click="dir = c.path"
          >
            {{ c.label }}
          </button>
        </template>
      </nav>
      <div v-if="selection.count.value" class="toolbar batch">
        <span>已选 {{ fmtNumber(selection.count.value) }} 项</span>
        <span class="spacer" />
        <button type="button" class="btn-sm" @click="askAssistant(selectedRows)">问助手</button>
        <button type="button" class="btn-sm" :disabled="resetting" @click="reset(selectedRows)">
          {{ resetting ? '保存中' : '重新整理' }}
        </button>
        <button type="button" class="ghost btn-sm" @click="selection.clear">取消选择</button>
      </div>
      <div v-else class="toolbar">
        <input
          v-model="list.filters.fq"
          type="search"
          class="search"
          placeholder="搜索全部文件"
          aria-label="搜索全部文件"
        />
        <Select v-model="list.filters.cat" :options="categoryOptions" label="分类" />
        <span class="spacer" />
        <button type="button" class="btn-icon" aria-label="刷新" :disabled="list.loading.value" @click="list.refresh">
          <Icon name="refresh-cw" />
        </button>
        <button type="button" @click="openLocate"><Icon name="search" />文件去哪了</button>
        <button type="button" @click="categoriesOpen = true">管理分类</button>
      </div>

      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th class="col-check">
                <input
                  type="checkbox"
                  aria-label="全选本页"
                  :checked="selection.all.value"
                  :indeterminate="selection.some.value"
                  :disabled="rows.length === 0"
                  @change="selection.toggleAll"
                />
              </th>
              <th>名称</th>
              <th>去向</th>
              <th class="num hide-sm">大小</th>
              <th class="col-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="f in rows"
              :key="f.id"
              class="clickable"
              tabindex="0"
              @click="open(f)"
              @keydown.enter.self.prevent="open(f)"
            >
              <td class="col-check" @click.stop>
                <input
                  type="checkbox"
                  :aria-label="`选择 ${f.name}`"
                  :checked="selection.has(f.id)"
                  @change="selection.toggle(f.id)"
                />
              </td>
              <td class="wrap">
                <span class="name">
                  <Icon :name="f.file_type === 'directory' ? 'folder' : 'file'" :size="14" />
                  <span class="truncate">{{ list.filters.fq ? shownPath(f.original_path) : f.name }}</span>
                  <StatusPill v-if="f.sensitive" value="sensitive" :map="ENTRY_FLAG" />
                </span>
                <span v-if="detailLine(f)" class="cell-sub">{{ detailLine(f) }}</span>
                <span v-if="showDate(f)" class="cell-sub">
                  {{ fmtLabel(DATE_SOURCE, f.date_source) }} {{ fmtDate(f.taken_at)
                  }}<template v-if="f.camera"> · {{ f.camera }}</template>
                </span>
              </td>
              <td class="wrap">
                <span class="flags">
                  <StatusPill :value="roleKey(f)" :map="ENTRY_ROLE" />
                  <StatusPill v-if="f.needs_review" value="needs_review" :map="ENTRY_FLAG" />
                </span>
                <span v-if="f.new_path" class="cell-sub">{{ landing(f.original_path, f.new_path) }}</span>
              </td>
              <td class="num hide-sm">
                {{ f.file_type === 'directory' ? `${fmtNumber(f.child_count)} 项` : fmtBytes(f.size) }}
              </td>
              <td class="row-actions">
                <span>
                  <button type="button" class="ghost btn-sm" @click.stop="editing = f">改位置</button>
                  <ActionMenu :items="menuFor(f)" />
                </span>
              </td>
            </tr>
            <TableState
              :colspan="5"
              :loading="list.loading.value && !rows.length"
              :error="list.error.value"
              :empty="rows.length === 0"
              :empty-text="filtered ? '没有符合条件的文件' : '这个文件夹是空的'"
              @retry="list.refresh"
            >
              <button v-if="filtered" type="button" class="btn-sm" @click="clearFilters">清除筛选</button>
            </TableState>
          </tbody>
        </table>
      </div>
      <Pagination
        :total="list.total.value"
        :page="list.page.value"
        :size="list.size.value"
        @update:page="list.setPage"
        @update:size="list.setSize"
      />
    </section>

    <FilePreviewDialog :file="previewing" @close="previewing = null" />
    <CategoryDialog
      :open="categoriesOpen"
      :filesystem-id="session.filesystem_id"
      @close="categoriesOpen = false"
      @changed="(categories.refresh(), list.refresh())"
    />

    <TargetDialog :entry="editing" @close="editing = null" @saved="(list.refresh(), emit('changed'))" />

    <BaseDialog :open="locate.open" title="文件去哪了" width="640px" @close="locate.open = false">
      <form class="toolbar" @submit.prevent="runLocate">
        <input
          v-model="locate.q"
          type="search"
          class="search"
          name="q"
          placeholder="原来的路径或文件名"
          aria-label="原来的路径或文件名"
        />
        <button type="submit" class="primary" :disabled="locate.loading">
          {{ locate.loading ? '查找中' : '查找' }}
        </button>
      </form>
      <Loading v-if="locate.loading" />
      <div v-else-if="locate.done" class="table-wrap">
        <table>
          <tbody>
            <tr v-for="r in locate.items" :key="r.entry_id">
              <td class="wrap">
                <span class="truncate">{{ shownPath(r.original_path) }}</span>
                <span class="cell-sub">{{ locateLine(r) }}</span>
              </td>
              <td v-if="r.op_state" class="num">
                <StatusPill :value="r.op_state" :map="OP_STATE" />
              </td>
            </tr>
            <TableState
              :colspan="2"
              :loading="false"
              error=""
              :empty="!locate.items.length"
              :empty-text="`没有找到「${locate.asked}」`"
            />
          </tbody>
        </table>
      </div>
    </BaseDialog>
  </div>
</template>

<style scoped>
.files {
  display: grid;
  grid-template-columns: 240px minmax(0, 1fr);
  gap: var(--space-3);
  align-items: start;
}
.files-crumbs {
  display: none;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px;
}
.files-main {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}
.name {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}
.flags {
  display: inline-flex;
  gap: var(--space-1);
}
@media (max-width: 760px) {
  .files {
    grid-template-columns: minmax(0, 1fr);
  }
  .files-tree {
    display: none;
  }
  .files-crumbs {
    display: flex;
  }
}
</style>
