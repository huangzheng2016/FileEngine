<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Loading from '@/base/Loading.vue'
import Segmented from '@/base/Segmented.vue'
import StatusPill from '@/base/StatusPill.vue'
import { fmtLabel, fmtNumber, labelOptions } from '@/base/format'
import { EXEC_MODE, ENTRY_FLAG, VALIDATION_ISSUE } from '@/base/labels'
import { vTip } from '@/base/tooltip'
import { toast } from '@/base/toast'
import { useFetch } from '@/base/useFetch'
import { useFormValidation } from '@/base/useFormValidation'
import { checkExecute, startExecute, type ExecMode } from '@/api/sessions'

// pending: moves that will run (entries awaiting review excluded); review: those awaiting review;
// resume: an earlier execute already ran, so the rest goes in one go.
const props = defineProps<{ open: boolean; sessionId: number; pending: number; review: number; resume: boolean }>()
const emit = defineEmits<{ close: []; started: [runId: number]; review: [] }>()

const form = reactive({ scope: 'sample' as 'sample' | 'all', sample: 20, mode: 'copy' as ExecMode })
const starting = ref(false)
const checkSample = () => (form.scope === 'sample' ? form.sample : 0)
const check = useFetch((signal) => checkExecute(props.sessionId, form.mode, checkSample(), signal))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, { scope: props.pending > 20 && !props.resume ? 'sample' : 'all', sample: 20, mode: 'copy' })
    v.reset()
    void check.refresh()
  },
)

watch(
  () => [form.mode, form.scope, form.sample],
  () => {
    if (props.open) void check.refresh()
  },
)

const v = useFormValidation(() => ({
  sample: form.scope === 'sample' && (!Number.isInteger(form.sample) || form.sample < 1) ? '请输入要先试的数量' : '',
}))

const scopes = computed(() => [
  { value: 'sample' as const, label: '先试一部分' },
  { value: 'all' as const, label: `全部 ${fmtNumber(props.pending)} 项` },
])
const MODES = labelOptions(EXEC_MODE) as { value: ExecMode; label: string }[]
const MODE_HINT: Record<ExecMode, string> = {
  copy: '在新位置放一份副本，原文件不动，通常会多占一份空间',
  move: '把文件挪到新位置，原来的位置不再保留',
}

const count = computed(() => (form.scope === 'sample' ? Math.min(form.sample, props.pending) : props.pending))
const blocked = computed(() => (props.pending ? '' : '没有可执行的项'))

async function submit() {
  if (!v.validate() || starting.value || blocked.value) return
  starting.value = true
  try {
    const runId = await startExecute(props.sessionId, form.mode, form.scope === 'sample' ? form.sample : 0)
    toast.success('已开始执行')
    emit('started', runId)
    emit('close')
  } catch (e) {
    toast.error('执行失败', e)
  } finally {
    starting.value = false
  }
}
</script>

<template>
  <BaseDialog :open="open" title="执行整理" width="560px" @close="emit('close')">
    <form id="execute-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <Loading v-if="check.loading.value" inline label="正在检查冲突" />
      <Alert v-else-if="check.error.value" tone="warning">检查冲突失败：{{ check.error.value }}</Alert>
      <template v-else-if="check.data.value && check.data.value.issues.length">
        <Alert tone="warning"
          >有 {{ fmtNumber(check.data.value.issues.length) }} 处冲突，这些文件执行时会被跳过。</Alert
        >
        <div class="table-wrap issues">
          <table>
            <tbody>
              <tr v-for="(i, n) in check.data.value.issues.slice(0, 50)" :key="n">
                <td><StatusPill :value="i.code" :map="VALIDATION_ISSUE" /></td>
                <td>
                  <span class="truncate">{{ i.new_path }}</span>
                  <span v-if="i.original_path" class="cell-sub">来自 {{ i.original_path }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <div class="field">
        <span class="field-label">方式</span>
        <Segmented v-model="form.mode" :options="MODES" label="方式" />
        <span class="field-hint">{{ MODE_HINT[form.mode] }}</span>
      </div>
      <div class="field">
        <span class="field-label">范围</span>
        <Segmented v-model="form.scope" :options="scopes" label="范围" />
        <span v-if="form.scope === 'sample'" class="field-hint">先处理一小部分，去存储里看过效果再执行其余的</span>
      </div>
      <label v-if="form.scope === 'sample'" class="field">
        <span class="field-label">先试的数量</span>
        <input v-model.number="form.sample" type="number" min="1" name="sample" :aria-invalid="!!v.error('sample')" />
        <span v-if="v.error('sample')" class="field-error">{{ v.error('sample') }}</span>
      </label>
      <Alert tone="info">
        将{{ fmtLabel(EXEC_MODE, form.mode) }} {{ fmtNumber(count) }} 项到新位置，完成后可以撤销。
        <template v-if="review">
          另有 {{ fmtNumber(review) }} 项{{ fmtLabel(ENTRY_FLAG, 'needs_review') }}，这次不会执行。
          <button type="button" class="ghost btn-sm" @click="emit('review')">去确认</button>
        </template>
      </Alert>
    </form>
    <template #footer>
      <button type="button" @click="emit('close')">取消</button>
      <button v-tip="blocked" type="submit" form="execute-form" class="primary" :disabled="starting || !!blocked">
        {{ starting ? '启动中' : `开始${fmtLabel(EXEC_MODE, form.mode)}` }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.issues {
  max-height: 200px;
}
</style>
