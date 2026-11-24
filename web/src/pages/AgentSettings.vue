<script setup lang="ts">
// Agent settings: one document saved to the server config; every field is read per run.
import { computed, reactive, ref } from 'vue'
import ActionMenu from '@/base/ActionMenu.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import CodeEditor from '@/base/CodeEditor.vue'
import DiffView from '@/base/DiffView.vue'
import EmptyState from '@/base/EmptyState.vue'
import Loading from '@/base/Loading.vue'
import Tabs from '@/base/Tabs.vue'
import UnitInput from '@/base/UnitInput.vue'
import { AGENT_SETTING_FIELDS } from '@/base/labels'
import { toast } from '@/base/toast'
import { vTip } from '@/base/tooltip'
import { useConfirm } from '@/base/useConfirm'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFetch } from '@/base/useFetch'
import { useFormValidation } from '@/base/useFormValidation'
import { useRouteQuery } from '@/base/useRouteQuery'
import { isConflict } from '@/api/http'
import { getConfig, getPrompt, saveConfig } from '@/api/settings'
import type { AppConfig } from '@/api/types'
import { categoryPresets } from '@/data/categoryPresets'
import SettingsHistory from './SettingsHistory.vue'

const confirm = useConfirm()

const TABS = [
  { key: 'form', label: '设置' },
  { key: 'history', label: '历史版本' },
] as const
type TabKey = (typeof TABS)[number]['key']
const { query, set } = useRouteQuery({ tab: 'form' })
const tab = computed<TabKey>({
  get: () => (query.value.tab === 'history' ? 'history' : 'form'),
  set: (t) => set({ tab: t }),
})

const form = reactive({
  concurrency: 6,
  max_step: 150,
  max_retries: 3,
  agent_timeout: 0,
  max_file_read_size: 0,
  category_policy: '',
  prompt: '',
})
const snapshot = ref<AppConfig | null>(null)
const defaultPrompt = ref('')
const defaultPolicy = ref('')
const guard = useDirtyGuard(() => form, { route: true })

const v = useFormValidation(() => ({
  concurrency: form.concurrency < 1 || form.concurrency > 32 ? '同时整理的文件夹数需在 1 到 32 之间' : '',
  max_file_read_size: form.max_file_read_size < 1000 ? '每次读取字数至少 1000' : '',
  max_step: form.max_step < 1 ? '单个文件夹最多步数至少为 1' : '',
  max_retries: form.max_retries < 0 || form.max_retries > 10 ? '失败重试需在 0 到 10 次之间' : '',
}))

const load = useFetch(
  async (signal) => {
    const [cfg, prompt] = await Promise.all([getConfig(signal), getPrompt(signal)])
    apply(cfg, prompt.default_prompt)
  },
  { immediate: true },
)

function apply(cfg: AppConfig, def = defaultPrompt.value) {
  snapshot.value = cfg
  defaultPrompt.value = def
  defaultPolicy.value = cfg.default_category_policy ?? ''
  const a = cfg.agent
  Object.assign(form, {
    concurrency: a.concurrency,
    max_step: a.max_step,
    max_retries: a.max_retries,
    agent_timeout: a.agent_timeout > 0 ? a.agent_timeout : 0,
    max_file_read_size: a.max_file_read_size,
    category_policy: a.category_policy ?? '',
    prompt: a.system_prompt || def,
  })
  v.reset()
  guard.reset()
  loaded = draft()
}

const saving = ref(false)
// The saved settings found after a stale save; the user's edits stay in the form meanwhile.
const conflict = ref<AppConfig | null>(null)

const draft = () => ({
  concurrency: form.concurrency,
  max_step: form.max_step,
  max_retries: form.max_retries,
  agent_timeout: form.agent_timeout,
  max_file_read_size: form.max_file_read_size,
  category_policy: form.category_policy.trim(),
  system_prompt: form.prompt.trim() === defaultPrompt.value.trim() ? '' : form.prompt,
})

// The values the form was loaded with: a save sends only what the user changed, so overwriting after
// a conflict keeps the other person's edits to every other field.
let loaded: ReturnType<typeof draft> | null = null
const changes = () => {
  const d = draft()
  return Object.fromEntries(
    Object.entries(d).filter(([k, val]) => !loaded || loaded[k as keyof typeof d] !== val),
  ) as Partial<ReturnType<typeof draft>>
}

async function save(base = snapshot.value?.version ?? 0) {
  if (!snapshot.value || !v.validate() || saving.value) return
  saving.value = true
  try {
    apply(await saveConfig(changes(), base))
    conflict.value = null
    toast.success('已保存')
  } catch (e) {
    if (!isConflict(e)) toast.error('保存失败', e)
    else {
      try {
        conflict.value = await getConfig()
      } catch (e2) {
        toast.error('设置已被其他人修改，读取最新设置失败', e2)
      }
    }
  } finally {
    saving.value = false
  }
}

function discardMine() {
  if (!conflict.value) return
  apply(conflict.value)
  conflict.value = null
  toast.success('已载入最新设置')
}

// The saved side and the draft side of the conflict diff, over the same fields.
const savedSide = computed(() => {
  const a = conflict.value?.agent
  if (!a) return {}
  const d = draft()
  return Object.fromEntries(
    Object.keys(d).map((k) => {
      const value = a[k as keyof typeof a] ?? ''
      return [k, k === 'system_prompt' && !value ? defaultPrompt.value : value]
    }),
  )
})
const draftSide = () => ({ ...draft(), system_prompt: form.prompt })
const versionText = computed(() =>
  snapshot.value?.version ? `版本 ${snapshot.value.version}` : '尚未保存过，当前为默认值',
)

async function revert() {
  if (snapshot.value && (await guard.confirmDiscard())) apply(snapshot.value)
}

const customPrompt = computed(() => form.prompt.trim() !== defaultPrompt.value.trim())
const promptOpen = ref(false)
function resetPrompt() {
  form.prompt = defaultPrompt.value
}

const PRESETS = categoryPresets.map((p) => ({ label: p.label, onSelect: () => void fillPolicy(p.id) }))
async function fillPolicy(id: string) {
  const p = categoryPresets.find((x) => x.id === id)
  if (!p) return
  if (
    form.category_policy.trim() &&
    !(await confirm({ message: `用「${p.label}」替换当前的分类规范？`, confirmText: '替换' }))
  )
    return
  form.category_policy = p.policy
}
</script>

<template>
  <Loading v-if="!snapshot && load.loading.value" />
  <EmptyState v-else-if="!snapshot && load.error.value" icon="alert-triangle" title="加载失败" :hint="load.error.value">
    <button type="button" @click="load.refresh">重试</button>
  </EmptyState>
  <div v-else-if="snapshot" class="page">
    <Tabs v-model="tab" :items="TABS">
      <template #form>
        <form class="page" novalidate v-on="v.form" @submit.prevent="save()">
          <p class="text-muted text-sm">{{ versionText }}</p>
          <section class="card">
            <div class="card-head"><h3>运行</h3></div>
            <div class="form-grid">
              <label class="field">
                <span class="field-label">同时整理的文件夹数</span>
                <input
                  v-model.number="form.concurrency"
                  type="number"
                  min="1"
                  max="32"
                  name="concurrency"
                  :aria-invalid="!!v.error('concurrency')"
                />
                <span v-if="v.error('concurrency')" class="field-error">{{ v.error('concurrency') }}</span>
              </label>
              <label class="field">
                <span class="field-label">单个文件夹最多步数</span>
                <input
                  v-model.number="form.max_step"
                  type="number"
                  min="1"
                  name="max_step"
                  :aria-invalid="!!v.error('max_step')"
                />
                <span v-if="v.error('max_step')" class="field-error">{{ v.error('max_step') }}</span>
              </label>
              <label class="field">
                <span class="field-label">失败重试</span>
                <input
                  v-model.number="form.max_retries"
                  type="number"
                  min="0"
                  max="10"
                  name="max_retries"
                  :aria-invalid="!!v.error('max_retries')"
                />
                <span v-if="v.error('max_retries')" class="field-error">{{ v.error('max_retries') }}</span>
              </label>
              <div class="field">
                <span class="field-label">单个文件夹时限</span>
                <UnitInput v-model="form.agent_timeout" kind="duration" name="agent_timeout" label="单个文件夹时限" />
                <span class="field-hint">留空不限</span>
              </div>
              <label class="field">
                <span class="field-label">每次读取字数</span>
                <input
                  v-model.number="form.max_file_read_size"
                  type="number"
                  min="1000"
                  name="max_file_read_size"
                  :aria-invalid="!!v.error('max_file_read_size')"
                />
                <span v-if="v.error('max_file_read_size')" class="field-error">{{
                  v.error('max_file_read_size')
                }}</span>
              </label>
            </div>
          </section>

          <section class="card">
            <div class="card-head">
              <h3>分类规范</h3>
              <ActionMenu :items="PRESETS" label="用模板填写" />
              <button
                v-if="form.category_policy.trim() !== defaultPolicy.trim()"
                type="button"
                class="btn-sm"
                @click="form.category_policy = defaultPolicy"
              >
                恢复默认
              </button>
            </div>
            <CodeEditor
              v-model="form.category_policy"
              wrap
              autogrow
              :rows="4"
              name="category_policy"
              placeholder="默认规范"
            />
          </section>

          <section class="card">
            <div class="card-head">
              <h3>提示词</h3>
              <span class="text-sm text-muted">{{ customPrompt ? '已自定义' : '默认' }}</span>
              <button v-if="customPrompt" type="button" class="btn-sm" @click="resetPrompt">恢复默认</button>
              <button type="button" class="btn-sm" :aria-expanded="promptOpen" @click="promptOpen = !promptOpen">
                {{ promptOpen ? '收起' : '编辑' }}
              </button>
            </div>
            <CodeEditor v-if="promptOpen" v-model="form.prompt" wrap autogrow name="prompt" />
          </section>

          <div class="save-bar">
            <span class="spacer" />
            <button type="button" :disabled="saving || !guard.dirty.value" @click="revert">撤销修改</button>
            <button
              v-tip="guard.dirty.value ? '' : '没有修改'"
              type="submit"
              class="primary"
              :disabled="saving || !guard.dirty.value"
            >
              {{ saving ? '保存中' : '保存' }}
            </button>
          </div>
        </form>
      </template>
      <template #history>
        <SettingsHistory :current-version="snapshot.version" />
      </template>
    </Tabs>

    <BaseDialog :open="!!conflict" title="设置已被其他人修改" width="720px" @close="conflict = null">
      <template v-if="conflict">
        <p>设置在你编辑时更新到了版本 {{ conflict.version }}。下表是它与你的修改的不同之处。</p>
        <DiffView
          :before="savedSide"
          :after="draftSide()"
          :labels="AGENT_SETTING_FIELDS"
          before-label="已保存"
          after-label="你的修改"
        />
      </template>
      <template #footer>
        <button type="button" @click="conflict = null">继续编辑</button>
        <button type="button" @click="discardMine">放弃我的修改</button>
        <button type="button" class="primary" :disabled="saving" @click="save(conflict!.version)">
          {{ saving ? '保存中' : '用我的修改覆盖' }}
        </button>
      </template>
    </BaseDialog>
  </div>
</template>
