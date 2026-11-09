<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import BaseDialog from '@/base/BaseDialog.vue'
import Combobox, { type ComboOption } from '@/base/Combobox.vue'
import Segmented from '@/base/Segmented.vue'
import Select from '@/base/Select.vue'
import Switch from '@/base/Switch.vue'
import { errorMessage } from '@/base/errors'
import { fmtNumber, labelOptions } from '@/base/format'
import { FILTER_MODE } from '@/base/labels'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { importCategories } from '@/api/files'
import { createRule } from '@/api/rules'
import { createSession } from '@/api/sessions'
import { listFilesystems, listModels, testFilesystem } from '@/api/settings'
import type { Filesystem, ModelProvider, ScanSession } from '@/api/types'
import { SCOPE_OPTIONS, TASK_PRESETS, scopeExts, type ScopeKey, type TaskPreset } from '@/data/presets'
import { joinExts, splitExts } from '@/lib/rules'
import FolderPicker from './FolderPicker.vue'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; created: [ScanSession] }>()

const empty = () => ({
  storage: null as ComboOption | null,
  scan_path: '',
  goal: '',
  preset: '',
  scope: 'all' as ScopeKey,
  custom_exts: '',
  model: 0,
  bulk_model: 0,
  allow_read_file: true,
  allow_auto_category: true,
  allow_vision: false,
  allow_script: true,
  extract_metadata: true,
  detect_duplicates: false,
  exclude_category_dirs: false,
  filter_mode: 'blacklist' as 'blacklist' | 'whitelist',
  filter_dirs: '',
})
const form = reactive(empty())
const advanced = ref(false)
const saving = ref(false)
const storages = ref<Filesystem[]>([])
const models = ref<ModelProvider[]>([])
const loaded = ref(false)

const guard = useDirtyGuard(() => form)
const v = useFormValidation(() => ({
  storage: form.storage ? '' : '请选择存储',
  scan_path: form.scan_path.split('/').includes('..') ? '目录不能包含 ..' : '',
  goal: form.goal.trim().length > 1000 ? '整理要求不超过 1000 个字符' : '',
  custom_exts: form.scope === 'custom' && !splitExts(form.custom_exts).length ? '请输入至少一个扩展名' : '',
}))

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    Object.assign(form, empty())
    advanced.value = false
    ownGoal = ''
    loaded.value = false
    v.reset()
    try {
      const [fs, ms] = await Promise.all([listFilesystems(), listModels()])
      storages.value = fs
      models.value = ms
      if (fs.length === 1) form.storage = { value: String(fs[0].id), label: fs[0].name }
      if (ms.length) form.model = ms[0].id
      loaded.value = true
    } catch (e) {
      toast.error('加载存储和模型失败', e)
    }
    guard.reset()
  },
)

const storageOptions = computed(() =>
  storages.value.map((f) => ({ value: String(f.id), label: f.name, hint: f.host || f.base_path })),
)
const modelOptions = computed(() => models.value.map((m) => ({ value: m.id, label: m.name, hint: m.model })))
const bulkOptions = computed(() => [{ value: 0, label: '同主模型' }, ...modelOptions.value])
const chosenModel = computed(() => models.value.find((m) => m.id === form.model))

const presetOptions = [{ value: '', label: '不用模板' }, ...TASK_PRESETS.map((p) => ({ value: p.id, label: p.name }))]
const preset = computed<TaskPreset | undefined>(() => TASK_PRESETS.find((p) => p.id === form.preset))

// The goal typed before a template was picked; a template only replaces an empty goal or its own.
let ownGoal = ''
function pick(id: string) {
  const prev = preset.value
  const next = TASK_PRESETS.find((p) => p.id === id)
  const untouched = !form.goal.trim() || form.goal === prev?.goal
  if (!prev) ownGoal = form.goal
  form.preset = id
  if (untouched) form.goal = next?.goal ?? ownGoal
  form.scope = next?.scope.key ?? 'all'
  form.custom_exts = next?.scope.key === 'custom' ? next.scope.exts.join(', ') : ''
}

async function close() {
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  if (!v.validate() || saving.value) return
  saving.value = true
  try {
    const exts = scopeExts({ key: form.scope, exts: splitExts(form.custom_exts) })
    const fsId = Number(form.storage!.value)
    const s = await createSession({
      filesystem_id: fsId,
      scan_path: form.scan_path.trim().replace(/^\/+|\/+$/g, ''),
      goal: form.goal.trim(),
      scope_exts: joinExts(exts),
      model_provider_id: form.model,
      bulk_model_provider_id: form.bulk_model,
      allow_read_file: form.allow_read_file,
      allow_auto_category: form.allow_auto_category,
      allow_vision: form.allow_vision,
      allow_script: form.allow_script,
      extract_metadata: form.extract_metadata,
      detect_duplicates: form.detect_duplicates,
      exclude_category_dirs: form.exclude_category_dirs,
      filter_mode: form.filter_mode,
      filter_dirs: form.filter_dirs,
      auto_tag: true,
    })
    const p = preset.value
    if (p) {
      try {
        for (const [i, r] of p.rules.entries()) await createRule(s.id, { ...r, position: i })
        if (p.categories.length) await importCategories(fsId, p.categories)
      } catch (e) {
        toast.error('任务已创建，模板规则未能全部新建', e)
      }
    }
    toast.success('已创建')
    guard.reset()
    emit('created', s)
    emit('close')
  } catch (e) {
    toast.error('创建失败', e)
  } finally {
    saving.value = false
  }
}

// ---- choosing the folder by browsing the storage ----

const chosenStorage = computed(() => storages.value.find((f) => String(f.id) === form.storage?.value))
const browser = reactive({
  open: false,
  path: '',
  dirs: [] as { name: string; path: string }[],
  loading: false,
  error: '',
})
let browseCtl: AbortController | null = null

const relOf = (fs: Filesystem, p: string) =>
  p
    .slice(fs.base_path.length)
    .replace(/\\/g, '/')
    .replace(/^\/+|\/+$/g, '')
const fullOf = (fs: Filesystem, rel: string) => {
  if (!rel) return fs.base_path
  const sep = fs.protocol === 'local' && fs.base_path.includes('\\') ? '\\' : '/'
  return fs.base_path.replace(/[\\/]+$/, '') + sep + rel.split('/').join(sep)
}

async function browse(path: string) {
  const fs = chosenStorage.value
  if (!fs) return
  browseCtl?.abort()
  const ctl = (browseCtl = new AbortController())
  Object.assign(browser, { open: true, path, loading: true, error: '' })
  try {
    const r = await testFilesystem(
      { id: fs.id, protocol: fs.protocol, host: fs.host, port: fs.port, username: fs.username, base_path: path },
      ctl.signal,
    )
    if (ctl.signal.aborted) return
    browser.dirs = r.dirs
    browser.error = r.success ? '' : (r.error ?? '读取失败')
  } catch (e) {
    if (!ctl.signal.aborted) Object.assign(browser, { dirs: [], error: errorMessage(e) })
  } finally {
    if (browseCtl === ctl) browser.loading = false
  }
}

function toggleBrowser() {
  const fs = chosenStorage.value
  if (browser.open || !fs) {
    browser.open = false
    return
  }
  void browse(fullOf(fs, form.scan_path.trim().replace(/^\/+|\/+$/g, '')))
}

function openFolder(path: string) {
  const fs = chosenStorage.value
  if (!fs) return
  form.scan_path = relOf(fs, path)
  void browse(path)
}

watch(
  () => form.storage?.value,
  () => {
    browseCtl?.abort()
    browser.open = false
  },
)

const FILTER_OPTIONS = labelOptions(FILTER_MODE) as { value: 'blacklist' | 'whitelist'; label: string }[]
const blocked = computed(() => (loaded.value && !models.value.length ? '先新建模型' : ''))
</script>

<template>
  <BaseDialog :open="open" title="新建整理任务" width="600px" @close="close">
    <form id="session-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <div class="form-grid">
        <div class="field">
          <span class="field-label required">存储</span>
          <Combobox
            v-model="form.storage"
            :options="storageOptions"
            placeholder="选择存储"
            :invalid="!!v.error('storage')"
          />
          <span v-if="v.error('storage')" class="field-error">{{ v.error('storage') }}</span>
          <span v-else-if="loaded && !storages.length" class="field-hint">
            还没有存储，<RouterLink to="/settings/storage">新建存储</RouterLink>
          </span>
        </div>
        <div class="field">
          <span class="field-label">目录</span>
          <div class="path-row">
            <input
              v-model="form.scan_path"
              name="scan_path"
              placeholder="留空整理全部"
              autocapitalize="off"
              spellcheck="false"
              :aria-invalid="!!v.error('scan_path')"
            />
            <button type="button" :disabled="!chosenStorage" @click="toggleBrowser">
              {{ browser.open ? '收起' : '浏览' }}
            </button>
          </div>
          <span v-if="v.error('scan_path')" class="field-error">{{ v.error('scan_path') }}</span>
        </div>
        <div v-if="browser.open && chosenStorage" class="field span-2">
          <FolderPicker
            :protocol="chosenStorage.protocol"
            :path="browser.path"
            :top="chosenStorage.base_path"
            :dirs="browser.dirs"
            :loading="browser.loading"
            :top-label="chosenStorage.name"
            @open="openFolder"
          />
          <span v-if="browser.error" class="field-error">{{ browser.error }}</span>
          <span v-else class="field-hint">点文件夹进入，进入的文件夹就是要整理的目录</span>
        </div>
        <label class="field span-2">
          <span class="field-label">整理要求</span>
          <textarea
            v-model="form.goal"
            name="goal"
            rows="3"
            placeholder="例如：照片按年份整理，项目文件夹保持完整"
            :aria-invalid="!!v.error('goal')"
          />
          <span v-if="v.error('goal')" class="field-error">{{ v.error('goal') }}</span>
        </label>
        <div class="field">
          <span class="field-label">模板</span>
          <Select :model-value="form.preset" :options="presetOptions" label="模板" @update:model-value="pick" />
          <span v-if="preset?.categories.length" class="field-hint">
            会给这个存储新建 {{ fmtNumber(preset.categories.length) }} 个分类
          </span>
        </div>
        <div class="field">
          <span class="field-label">整理范围</span>
          <Select v-model="form.scope" :options="SCOPE_OPTIONS" label="整理范围" />
        </div>
        <label v-if="form.scope === 'custom'" class="field span-2">
          <span class="field-label required">扩展名</span>
          <input
            v-model="form.custom_exts"
            name="custom_exts"
            placeholder="jpg, png, mp4"
            :aria-invalid="!!v.error('custom_exts')"
          />
          <span v-if="v.error('custom_exts')" class="field-error">{{ v.error('custom_exts') }}</span>
          <span v-else class="field-hint">范围外的文件保持不动</span>
        </label>
      </div>
      <p v-if="blocked" class="field-hint">还没有模型，<RouterLink to="/settings/models">新建模型</RouterLink></p>

      <details :open="advanced" @toggle="advanced = ($event.target as HTMLDetailsElement).open">
        <summary>高级选项</summary>
        <div class="form-grid advanced">
          <div v-if="models.length" class="field">
            <span class="field-label">模型</span>
            <Select v-model="form.model" :options="modelOptions" label="模型" placeholder="选择模型" />
          </div>
          <div class="field">
            <span class="field-label">简单文件用的模型</span>
            <Select v-model="form.bulk_model" :options="bulkOptions" label="简单文件用的模型" />
          </div>
          <div class="field">
            <span class="field-label">读取文件内容</span>
            <Switch v-model="form.allow_read_file" label="读取文件内容" />
          </div>
          <div class="field">
            <span class="field-label">自动新建分类</span>
            <Switch v-model="form.allow_auto_category" label="自动新建分类" />
          </div>
          <div class="field">
            <span class="field-label">查看图片</span>
            <Switch v-model="form.allow_vision" :disabled="!chosenModel?.vision_enabled" label="查看图片" />
            <span v-if="!chosenModel?.vision_enabled" class="field-hint">所选模型不能读图</span>
          </div>
          <div class="field">
            <span class="field-label">允许运行脚本</span>
            <Switch v-model="form.allow_script" label="允许运行脚本" />
          </div>
          <div class="field">
            <span class="field-label">读取拍摄信息</span>
            <Switch v-model="form.extract_metadata" label="读取拍摄信息" />
          </div>
          <div class="field">
            <span class="field-label">查找重复文件</span>
            <Switch v-model="form.detect_duplicates" label="查找重复文件" />
            <span class="field-hint">扫描会变慢</span>
          </div>
          <div class="field">
            <span class="field-label">跳过已有的分类目录</span>
            <Switch v-model="form.exclude_category_dirs" label="跳过已有的分类目录" />
          </div>
          <div class="field span-2">
            <span class="field-label">目录过滤</span>
            <Segmented v-model="form.filter_mode" :options="FILTER_OPTIONS" label="目录过滤" />
            <textarea
              v-model="form.filter_dirs"
              name="filter_dirs"
              rows="3"
              placeholder="/backup"
              aria-label="过滤的目录"
            />
            <span class="field-hint">每行一个目录</span>
          </div>
        </div>
      </details>
    </form>
    <template #footer>
      <button type="button" @click="close">取消</button>
      <button v-tip="blocked" type="submit" form="session-form" class="primary" :disabled="saving || !!blocked">
        {{ saving ? '创建中' : '创建并整理' }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.path-row {
  display: flex;
  gap: var(--space-2);
}
.path-row input {
  flex: 1;
  min-width: 0;
}
.advanced {
  margin-top: var(--space-3);
}
</style>
