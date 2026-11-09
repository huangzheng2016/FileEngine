<script setup lang="ts">
import { ref, watch } from 'vue'
import BaseDialog from '@/base/BaseDialog.vue'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { updateFileTarget } from '@/api/files'

export interface TargetEntry {
  id: number
  original_path: string
  new_path: string
}

const props = defineProps<{ entry: TargetEntry | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const path = ref('')
const saving = ref(false)
const guard = useDirtyGuard(() => path.value)
const v = useFormValidation(() => ({
  path: path.value.split('/').includes('..') ? '新位置不能包含 ..' : '',
}))

watch(
  () => props.entry,
  (e) => {
    if (!e) return
    path.value = e.new_path.replace(/^\/+/, '')
    v.reset()
    guard.reset()
  },
)

async function close() {
  if (await guard.confirmDiscard()) emit('close')
}

async function save() {
  if (!props.entry || !v.validate() || saving.value) return
  saving.value = true
  try {
    const target = path.value.trim()
    await updateFileTarget(props.entry.id, target)
    toast.success(target ? '已保存' : '已设为保持原位')
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
  <BaseDialog :open="!!entry" title="修改新位置" width="520px" @close="close">
    <form id="target-form" class="form" novalidate v-on="v.form" @submit.prevent="save">
      <p class="text-muted mono">{{ entry?.original_path }}</p>
      <label class="field">
        <span class="field-label">新位置</span>
        <input
          v-model="path"
          name="path"
          class="mono"
          placeholder="照片/2023/海边.jpg"
          :aria-invalid="!!v.error('path')"
        />
        <span v-if="v.error('path')" class="field-error">{{ v.error('path') }}</span>
        <span v-else class="field-hint">留空表示保持原位</span>
      </label>
    </form>
    <template #footer>
      <button type="button" @click="close">取消</button>
      <button type="submit" form="target-form" class="primary" :disabled="saving">
        {{ saving ? '保存中' : '保存' }}
      </button>
    </template>
  </BaseDialog>
</template>
