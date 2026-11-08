<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Icon from '@/base/Icon.vue'
import Combobox, { type ComboOption } from '@/base/Combobox.vue'
import Segmented from '@/base/Segmented.vue'
import Select from '@/base/Select.vue'
import Switch from '@/base/Switch.vue'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { importCategories } from '@/api/files'
import { createRule } from '@/api/rules'
import { createSession } from '@/api/sessions'
import { listFilesystems, listModels } from '@/api/settings'
import type { Filesystem, ModelProvider, ScanSession } from '@/api/types'
import { SCOPE_OPTIONS, TASK_PRESETS, scopeExts, type ScopeKey, type TaskPreset } from '@/data/presets'
import { joinExts, splitExts } from '@/lib/rules'

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

const guard = useDirtyGuard(() => form)
const v = useFormValidation(() => ({
  storage: form.storage ? '' : '请选择存储',
  scan_path: form.scan_path.split('/').includes('..') ? '目录不能包含 ..' : '',
  goal: form.goal.trim().length > 1000 ? '整理目标不超过 1000 个字符' : '',
  custom_exts: form.scope === 'custom' && !splitExts(form.custom_exts).length ? '请输入至少一个扩展名' : '',
}))

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    Object.assign(form, empty())
    advanced.value = false
    v.reset()
    try {
      const [fs, ms] = await Promise.all([listFilesystems(), listModels()])
      storages.value = fs
      models.value = ms
      if (fs.length === 1) form.storage = { value: String(fs[0].id), label: fs[0].name }
      if (ms.length) form.model = ms[0].id
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

const preset = computed<TaskPreset | undefined>(() => TASK_PRESETS.find((p) => p.id === form.preset))

function pick(p: TaskPreset) {
  if (form.preset === p.id) {
    form.preset = ''
    return
  }
  form.preset = p.id
  form.goal = p.goal
  form.scope = p.scope.key
  form.custom_exts = p.scope.key === 'custom' ? p.scope.exts.join(', ') : ''
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
    })
    const p = preset.value
    if (p) {
      try {
        for (const [i, r] of p.rules.entries()) await createRule(s.id, { ...r, position: i })
        if (p.categories.length) await importCategories(fsId, p.categories)
      } catch (e) {
        toast.error('任务已创建，模板规则未能全部添加', e)
      }
    }
    toast.success('已创建，开始扫描')
    guard.reset()
    emit('created', s)
    emit('close')
  } catch (e) {
    toast.error('创建失败', e)
  } finally {
    saving.value = false
  }
}

const FILTER_OPTIONS = [
  { value: 'blacklist' as const, label: '跳过这些目录' },
  { value: 'whitelist' as const, label: '只扫描这些目录' },
]
</script>

<template>
  <BaseDialog :open="open" title="新建整理任务" width="600px" @close="close">
    <form id="session-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <div class="presets" role="group" aria-label="模板">
        <button
          v-for="p in TASK_PRESETS"
          :key="p.id"
          type="button"
          class="btn-sm"
          :aria-pressed="form.preset === p.id"
          @click="pick(p)"
        >
          <Icon v-if="form.preset === p.id" name="check" :size="14" />{{ p.name }}
        </button>
      </div>
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
          <span v-else-if="!storages.length" class="field-hint">先在 设置 / 存储 中添加</span>
        </div>
        <label class="field">
          <span class="field-label">目录</span>
          <input
            v-model="form.scan_path"
            name="scan_path"
            placeholder="留空整理全部"
            :aria-invalid="!!v.error('scan_path')"
          />
          <span v-if="v.error('scan_path')" class="field-error">{{ v.error('scan_path') }}</span>
        </label>
        <label class="field span-2">
          <span class="field-label">整理目标</span>
          <textarea
            v-model="form.goal"
            name="goal"
            rows="3"
            placeholder="例如：照片按年份整理，项目文件夹保持完整"
            :aria-invalid="!!v.error('goal')"
          />
          <span v-if="v.error('goal')" class="field-error">{{ v.error('goal') }}</span>
        </label>
        <div class="field span-2">
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

      <details :open="advanced" @toggle="advanced = ($event.target as HTMLDetailsElement).open">
        <summary>高级选项</summary>
        <div class="form-grid advanced">
          <div class="field">
            <span class="field-label">模型</span>
            <Select v-model="form.model" :options="modelOptions" label="模型" placeholder="使用默认模型" />
          </div>
          <div class="field">
            <span class="field-label">批量处理模型</span>
            <Select v-model="form.bulk_model" :options="bulkOptions" label="批量处理模型" />
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
            <span class="field-label">批量脚本</span>
            <Switch v-model="form.allow_script" label="批量脚本" />
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
            <span class="field-label">跳过分类目录</span>
            <Switch v-model="form.exclude_category_dirs" label="跳过分类目录" />
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
      <button type="submit" form="session-form" class="primary" :disabled="saving">
        {{ saving ? '创建中' : '创建并扫描' }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.presets {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.advanced {
  margin-top: var(--space-3);
}
</style>
