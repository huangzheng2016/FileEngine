<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import BaseDialog from '@/base/BaseDialog.vue'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { updateSession } from '@/api/sessions'
import type { Id } from '@/api/types'

const props = defineProps<{ open: boolean; sessionId: Id; goal: string }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const form = reactive({ text: '' })
const saving = ref(false)
const guard = useDirtyGuard(() => form.text)
const check = useFormValidation(() => ({
  goal: form.text.trim().length > 1000 ? '整理要求不超过 1000 个字符' : '',
}))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    form.text = props.goal
    guard.reset()
    check.reset()
  },
)

async function close() {
  if (await guard.confirmDiscard()) emit('close')
}

async function save() {
  if (!check.validate() || saving.value) return
  saving.value = true
  try {
    await updateSession(props.sessionId, { goal: form.text.trim() })
    toast.success('已保存')
    guard.reset()
    emit('saved')
    emit('close')
  } catch (e) {
    toast.error('保存失败', e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseDialog :open="open" title="修改整理要求" width="520px" @close="close">
    <form id="goal-form" class="form" novalidate v-on="check.form" @submit.prevent="save">
      <label class="field">
        <textarea
          v-model="form.text"
          rows="4"
          name="goal"
          aria-label="整理要求"
          placeholder="例如：照片按年份整理，项目文件夹保持完整"
          :aria-invalid="!!check.error('goal')"
        />
        <span v-if="check.error('goal')" class="field-error">{{ check.error('goal') }}</span>
      </label>
    </form>
    <template #footer>
      <button type="button" @click="close">取消</button>
      <button type="submit" form="goal-form" class="primary" :disabled="saving">
        {{ saving ? '保存中' : '保存' }}
      </button>
    </template>
  </BaseDialog>
</template>
