<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Loading from '@/base/Loading.vue'
import Segmented from '@/base/Segmented.vue'
import StatusPill from '@/base/StatusPill.vue'
import { fmtNumber } from '@/base/format'
import { VALIDATION_ISSUE } from '@/base/labels'
import { toast } from '@/base/toast'
import { useFetch } from '@/base/useFetch'
import { useFormValidation } from '@/base/useFormValidation'
import { startExecute, validatePlans, type ExecMode } from '@/api/sessions'

const props = defineProps<{ open: boolean; sessionId: number; pending: number }>()
const emit = defineEmits<{ close: []; started: [runId: number] }>()

const form = reactive({ scope: 'sample' as 'sample' | 'all', sample: 20, mode: 'copy' as ExecMode })
const starting = ref(false)
const check = useFetch((signal) => validatePlans(props.sessionId, signal))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, { scope: props.pending > 20 ? 'sample' : 'all', sample: 20, mode: 'copy' })
    v.reset()
    void check.refresh()
  },
)

const v = useFormValidation(() => ({
  sample: form.scope === 'sample' && (!Number.isInteger(form.sample) || form.sample < 1) ? '请输入要先执行的数量' : '',
}))

const SCOPES = [
  { value: 'sample' as const, label: '先试运行一部分' },
  { value: 'all' as const, label: '全部执行' },
]
const MODES = [
  { value: 'copy' as const, label: '复制' },
  { value: 'move' as const, label: '移动' },
]

const count = computed(() => (form.scope === 'sample' ? Math.min(form.sample, props.pending) : props.pending))

async function submit() {
  if (!v.validate() || starting.value) return
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
  <BaseDialog :open="open" title="执行整理方案" width="560px" @close="emit('close')">
    <form id="execute-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <Loading v-if="check.loading.value" inline label="正在检查冲突" />
      <Alert v-else-if="check.error.value" tone="warning">检查冲突失败：{{ check.error.value }}</Alert>
      <template v-else-if="check.data.value && check.data.value.issues.length">
        <Alert tone="warning">有 {{ check.data.value.issues.length }} 处冲突，这些文件执行时会被跳过。</Alert>
        <div class="table-wrap issues">
          <table>
            <tbody>
              <tr v-for="(i, n) in check.data.value.issues.slice(0, 50)" :key="n">
                <td><StatusPill :value="i.issue" :map="VALIDATION_ISSUE" /></td>
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
        <span class="field-label">范围</span>
        <Segmented v-model="form.scope" :options="SCOPES" label="范围" />
      </div>
      <label v-if="form.scope === 'sample'" class="field">
        <span class="field-label">先执行的数量</span>
        <input v-model.number="form.sample" type="number" min="1" name="sample" :aria-invalid="!!v.error('sample')" />
        <span v-if="v.error('sample')" class="field-error">{{ v.error('sample') }}</span>
        <span v-else class="field-hint">确认结果没问题后再执行其余部分</span>
      </label>
      <div class="field">
        <span class="field-label">方式</span>
        <Segmented v-model="form.mode" :options="MODES" label="方式" />
        <span class="field-hint">{{
          form.mode === 'copy' ? '原文件保留；支持写时复制的存储几乎不占额外空间' : '原文件会被移走，执行后可以撤销'
        }}</span>
      </div>
      <p class="tabular">将{{ form.mode === 'copy' ? '复制' : '移动' }} {{ fmtNumber(count) }} 项。</p>
    </form>
    <template #footer>
      <button type="button" @click="emit('close')">取消</button>
      <button type="submit" form="execute-form" class="primary" :disabled="starting">
        {{ starting ? '启动中' : '开始执行' }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.issues {
  max-height: 200px;
}
</style>
