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
import { DATE_SOURCE, ENTRY_FLAG, ENTRY_ROLE } from '@/base/labels'
import { fmtDate } from '@/base/time'
import { toast } from '@/base/toast'
import { useFetch } from '@/base/useFetch'
import { useRouteQuery } from '@/base/useRouteQuery'
import { useSelection } from '@/base/useSelection'
import { listCategories, listFiles, resetPlans } from '@/api/files'
import { toSec } from '@/api/http'
import { locateFile } from '@/api/sessions'
import type { FileEntry, LocateResult, ScanSession } from '@/api/types'
import DirTree from '@/components/DirTree.vue'
import { addChatFiles } from '@/lib/chatFiles'
import { crumbs } from '@/lib/paths'
import { landing } from '@/lib/toolView'
import CategoryDialog from './CategoryDialog.vue'
import FilePreviewDialog from './FilePreviewDialog.vue'
import TargetDialog from './TargetDialog.vue'

const props = defineProps<{ session: ScanSession }>()
const emit = defineEmits<{ changed: [] }>()

const SIZE = 50
const { query, set } = useRouteQuery({ dir: '', fq: '', cat: '', fp: 1 })
const search = ref(query.value.fq)
let timer: ReturnType<typeof setTimeout> | undefined
watch(search, (q) => {
  clearTimeout(timer)
  timer = setTimeout(() => set({ fq: q.trim(), fp: 1 }), 300)
})

const files = useFetch((signal) =>
  listFiles(
    {
      session_id: props.session.id,
      parent_path: query.value.dir,
      search: query.value.fq,
      category_path: query.value.cat,
      page: query.value.fp,
      size: SIZE,
    },
    signal,
  ),
)
watch(
  () => [props.session.id, query.value.dir, query.value.fq, query.value.cat, query.value.fp],
  () => void files.refresh(),
  { immediate: true },
)
const rows = computed(() => files.data.value?.items ?? [])

const categories = useFetch((signal) => listCategories(props.session.filesystem_id, signal), { immediate: true })
const categoryOptions = computed(() => [
  { value: '', label: '全部分类' },
  ...(categories.data.value ?? []).map((c) => ({ value: c.path, label: c.name })),
])

const dir = computed({
  get: () => query.value.dir,
  set: (d: string) => set({ dir: d, fp: 1, fq: '' }),
})
watch(
  () => query.value.fq,
  (q) => {
    if (q !== search.value.trim()) search.value = q
  },
)

const selection = useSelection(computed(() => rows.value.map((r) => ({ id: String(r.id) }))))
const selectedRows = computed(() => rows.value.filter((r) => selection.has(String(r.id))))

function roleKey(f: FileEntry) {
  return f.role || 'pending'
}

function detailLine(f: FileEntry): string {
  if (f.role === 'container') return ''
  if (f.reason) return f.reason
  if (f.role === 'member') return '随上级文件夹整体移动'
  return ''
}

// mtime is a fallback every file has; only real capture dates are worth a line.
const showDate = (f: FileEntry) => !!f.taken_at && f.date_source !== 'mtime'

const trail = computed(() => crumbs(query.value.dir))

function open(f: FileEntry) {
  if (f.file_type === 'directory') dir.value = f.original_path
  else previewing.value = f
}

const previewing = ref<FileEntry | null>(null)
const categoriesOpen = ref(false)

function askAssistant(list: FileEntry[]) {
  addChatFiles(
    props.session.id,
    list.map((f) => ({ id: f.id, name: f.name })),
  )
  toast.success(`已把 ${fmtNumber(list.length)} 项加入对话`)
  selection.clear()
}

const resetting = ref(false)
async function reset(list: FileEntry[]) {
  if (resetting.value) return
  resetting.value = true
  try {
    await resetPlans(
      props.session.id,
      list.map((f) => f.id),
    )
    toast.success(list.length === 1 ? '已交给 AI 重新整理' : `已把 ${fmtNumber(list.length)} 项交给 AI 重新整理`)
    selection.clear()
    void files.refresh()
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

const editing = ref<FileEntry | null>(null)

const locate = reactive({ open: false, q: '', loading: false, done: false, items: [] as LocateResult[] })
async function runLocate() {
  const q = locate.q.trim()
  if (!q) return
  locate.loading = true
  try {
    locate.items = await locateFile(props.session.id, q)
    locate.done = true
  } catch (e) {
    toast.error('查找失败', e)
  } finally {
    locate.loading = false
  }
}
function openLocate() {
  Object.assign(locate, { open: true, q: '', done: false, items: [] })
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
        <input v-model="search" type="search" class="search" placeholder="搜索文件名" aria-label="搜索文件名" />
        <Select
          :model-value="query.cat"
          :options="categoryOptions"
          label="分类"
          @update:model-value="set({ cat: $event, fp: 1 })"
        />
        <span class="spacer" />
        <button type="button" @click="openLocate"><Icon name="search" />文件去哪了</button>
        <button type="button" @click="categoriesOpen = true">分类</button>
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
                  :checked="selection.has(String(f.id))"
                  @change="selection.toggle(String(f.id))"
                />
              </td>
              <td class="wrap">
                <span class="name">
                  <Icon :name="f.file_type === 'directory' ? 'folder' : 'file'" :size="14" />
                  <span class="truncate">{{ query.fq ? f.original_path : f.name }}</span>
                  <StatusPill v-if="f.sensitive" value="sensitive" :map="ENTRY_FLAG" />
                </span>
                <span v-if="detailLine(f)" class="cell-sub">{{ detailLine(f) }}</span>
                <span v-if="showDate(f)" class="cell-sub">
                  {{ fmtLabel(DATE_SOURCE, f.date_source || 'exif') }} {{ fmtDate(toSec(f.taken_at))
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
              <td class="num hide-sm">{{ f.file_type === 'directory' ? '—' : fmtBytes(f.size) }}</td>
              <td class="row-actions">
                <span>
                  <button type="button" class="ghost btn-sm" @click.stop="editing = f">改位置</button>
                  <ActionMenu :items="menuFor(f)" />
                </span>
              </td>
            </tr>
            <TableState
              :colspan="5"
              :loading="files.loading.value && !rows.length"
              :error="files.error.value"
              :empty="rows.length === 0"
              :empty-text="query.fq || query.cat ? '没有符合条件的文件' : '这个文件夹是空的'"
              @retry="files.refresh"
            >
              <button v-if="query.fq || query.cat" type="button" class="btn-sm" @click="set({ fq: '', cat: '' })">
                清除筛选
              </button>
            </TableState>
          </tbody>
        </table>
      </div>
      <Pagination
        v-if="(files.data.value?.total ?? 0) > SIZE"
        :total="files.data.value?.total ?? 0"
        :page="query.fp"
        :size="SIZE"
        @update:page="set({ fp: $event })"
      />
    </section>

    <FilePreviewDialog :file="previewing" @close="previewing = null" />
    <CategoryDialog
      :open="categoriesOpen"
      :filesystem-id="session.filesystem_id"
      @close="categoriesOpen = false"
      @changed="(categories.refresh(), files.refresh())"
    />

    <TargetDialog :entry="editing" @close="editing = null" @saved="(files.refresh(), emit('changed'))" />

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
        <button type="submit" class="primary" :disabled="locate.loading">查找</button>
      </form>
      <Loading v-if="locate.loading" />
      <div v-else-if="locate.done" class="table-wrap">
        <table>
          <tbody>
            <tr v-for="r in locate.items" :key="r.entry_id">
              <td>
                <span class="truncate">{{ r.original_path }}</span>
                <span class="cell-sub">
                  {{ r.new_path ? `${r.executed ? '已移到' : '将移到'} ${r.new_path}` : '不移动' }}
                </span>
              </td>
            </tr>
            <TableState :colspan="1" :loading="false" error="" :empty="!locate.items.length" empty-text="没有找到" />
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
.files-tree {
  padding: var(--space-2);
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
