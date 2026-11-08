<script setup lang="ts">
// Files tab: the scanned tree with each entry's decision. Correcting a target here is
// remembered for next time.
import { computed, reactive, ref, watch } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Icon from '@/base/Icon.vue'
import Loading from '@/base/Loading.vue'
import Pagination from '@/base/Pagination.vue'
import Select from '@/base/Select.vue'
import StatusPill from '@/base/StatusPill.vue'
import TableState from '@/base/TableState.vue'
import { fmtBytes, fmtLabel } from '@/base/format'
import { DATE_SOURCE, ENTRY_FLAG, ENTRY_ROLE } from '@/base/labels'
import { fmtDate } from '@/base/time'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFetch } from '@/base/useFetch'
import { useFormValidation } from '@/base/useFormValidation'
import { useRouteQuery } from '@/base/useRouteQuery'
import { useSelection } from '@/base/useSelection'
import { listCategories, listFiles, resetPlans, updateFileTarget } from '@/api/files'
import { toSec } from '@/api/http'
import { locateFile } from '@/api/sessions'
import type { FileEntry, LocateResult, ScanSession } from '@/api/types'
import DirTree from '@/components/DirTree.vue'
import { addChatFiles } from '@/lib/chatFiles'
import CategoryDialog from './CategoryDialog.vue'
import FilePreviewDialog from './FilePreviewDialog.vue'

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
  if (f.reason) return f.reason
  if (f.role === 'member') return '随上级目录整体移动'
  return ''
}

function open(f: FileEntry) {
  if (f.file_type === 'directory') dir.value = f.original_path
  else previewing.value = f
}

// ---------- actions ----------

const previewing = ref<FileEntry | null>(null)
const categoriesOpen = ref(false)

function askAssistant(list: FileEntry[]) {
  addChatFiles(
    props.session.id,
    list.map((f) => ({ id: f.id, name: f.name })),
  )
  toast.success(`已把 ${list.length} 项加入对话`)
  selection.clear()
}

async function sendBack(list: FileEntry[]) {
  try {
    await resetPlans(
      props.session.id,
      list.map((f) => f.id),
    )
    toast.success(list.length === 1 ? '已退回' : `已退回 ${list.length} 项`)
    selection.clear()
    void files.refresh()
    emit('changed')
  } catch (e) {
    toast.error('退回失败', e)
  }
}

const menuFor = (f: FileEntry) => [
  { label: '预览', icon: 'eye' as const, onSelect: () => (previewing.value = f) },
  { label: '问助手', icon: 'message-square' as const, onSelect: () => askAssistant([f]) },
  {
    label: '退回',
    icon: 'rotate-ccw' as const,
    disabled: !f.new_path,
    title: f.new_path ? undefined : '还没有目标位置',
    onSelect: () => void sendBack([f]),
  },
]

// ---------- target ----------

const target = reactive({ file: null as FileEntry | null, path: '', saving: false })
const targetGuard = useDirtyGuard(() => target.path)
const tv = useFormValidation(() => ({
  path: target.path.split('/').includes('..') ? '目标位置不能包含 ..' : '',
}))

function editTarget(f: FileEntry) {
  target.file = f
  target.path = f.new_path
  tv.reset()
  targetGuard.reset()
}
async function closeTarget() {
  if (await targetGuard.confirmDiscard()) target.file = null
}
async function saveTarget() {
  if (!target.file || !tv.validate() || target.saving) return
  target.saving = true
  try {
    await updateFileTarget(target.file.id, target.path.trim())
    toast.success(target.path.trim() ? '已记住此修正' : '已清除目标')
    targetGuard.reset()
    target.file = null
    void files.refresh()
    emit('changed')
  } catch (e) {
    toast.error('保存失败', e)
  } finally {
    target.saving = false
  }
}

// ---------- locate ----------

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
      <div v-if="selection.count.value" class="toolbar batch">
        <span>已选 {{ selection.count.value }} 项</span>
        <span class="spacer" />
        <button type="button" class="btn-sm" @click="askAssistant(selectedRows)">问助手</button>
        <button type="button" class="btn-sm" @click="sendBack(selectedRows)">退回</button>
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
              <th>决定</th>
              <th>目标位置</th>
              <th class="num">大小</th>
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
              <td>
                <span class="name">
                  <Icon :name="f.file_type === 'directory' ? 'folder' : 'file'" :size="14" />
                  <span class="truncate">{{ query.fq ? f.original_path : f.name }}</span>
                  <StatusPill v-if="f.sensitive" value="sensitive" :map="ENTRY_FLAG" />
                </span>
                <span v-if="detailLine(f)" class="cell-sub">{{ detailLine(f) }}</span>
                <span v-if="f.taken_at" class="cell-sub">
                  {{ fmtLabel(DATE_SOURCE, f.date_source || 'exif') }} {{ fmtDate(toSec(f.taken_at))
                  }}<template v-if="f.camera"> · {{ f.camera }}</template>
                </span>
              </td>
              <td>
                <span class="flags">
                  <StatusPill :value="roleKey(f)" :map="ENTRY_ROLE" />
                  <StatusPill v-if="f.needs_review" value="needs_review" :map="ENTRY_FLAG" />
                </span>
              </td>
              <td>
                <span class="truncate">{{ f.new_path || '—' }}</span>
              </td>
              <td class="num">{{ f.file_type === 'directory' ? '—' : fmtBytes(f.size) }}</td>
              <td class="row-actions">
                <span>
                  <button type="button" class="ghost btn-sm" @click.stop="editTarget(f)">改目标</button>
                  <ActionMenu :items="menuFor(f)" />
                </span>
              </td>
            </tr>
            <TableState
              :colspan="6"
              :loading="files.loading.value && !rows.length"
              :error="files.error.value"
              :empty="rows.length === 0"
              :empty-text="query.fq || query.cat ? '没有符合条件的文件' : '这个目录是空的'"
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

    <BaseDialog :open="!!target.file" title="修改目标位置" width="520px" @close="closeTarget">
      <form id="target-form" class="form" novalidate v-on="tv.form" @submit.prevent="saveTarget">
        <p class="text-muted mono">{{ target.file?.original_path }}</p>
        <label class="field">
          <span class="field-label">目标位置</span>
          <input
            v-model="target.path"
            name="path"
            class="mono"
            placeholder="照片/2023/海边.jpg"
            :aria-invalid="!!tv.error('path')"
          />
          <span v-if="tv.error('path')" class="field-error">{{ tv.error('path') }}</span>
          <span v-else class="field-hint">留空表示不移动</span>
        </label>
      </form>
      <template #footer>
        <button type="button" @click="closeTarget">取消</button>
        <button type="submit" form="target-form" class="primary" :disabled="target.saving">
          {{ target.saving ? '保存中' : '保存' }}
        </button>
      </template>
    </BaseDialog>

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
  max-height: 75vh;
  padding: var(--space-2);
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
}
</style>
