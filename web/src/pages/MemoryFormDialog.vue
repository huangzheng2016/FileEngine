<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Select from '@/base/Select.vue'
import { labelOptions } from '@/base/format'
import { MEMORY_KIND } from '@/base/labels'
import { toast } from '@/base/toast'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import { useFormValidation } from '@/base/useFormValidation'
import { createMemory, updateMemory } from '@/api/memory'
import type { MemoryItem, MemoryKind, MemoryScope } from '@/api/types'

const props = defineProps<{ open: boolean; editing: MemoryItem | null; scope: MemoryScope; scopeId: number }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const empty = () => ({ kind: 'preference' as MemoryKind, content: '', path: '' })
const form = reactive(empty())
const saving = ref(false)
const guard = useDirtyGuard(() => form)
const v = useFormValidation(() => ({
  content: !form.content.trim() ? '请输入内容' : form.content.trim().length > 300 ? '内容不超过 300 个字符' : '',
}))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    const m = props.editing
    Object.assign(form, m ? { kind: m.kind, content: m.content, path: m.path } : empty())
    v.reset()
    guard.reset()
  },
)

const KINDS = labelOptions(MEMORY_KIND).filter((o) => o.value !== 'note') as { value: MemoryKind; label: string }[]
const title = computed(() => (props.editing ? '编辑记忆' : '新建记忆'))
// The kind only matters for what the assistant wrote; a user note is a preference.
const showKind = computed(() => !!props.editing && props.editing.source !== 'user')

// A user-written item gets a key from its content so a second identical note updates it.
function keyOf(content: string, path: string) {
  return path.trim() ? `path:${path.trim().replace(/^\/+/, '')}` : `user:${content.trim().slice(0, 48)}`
}

async function close() {
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  if (!v.validate() || saving.value) return
  saving.value = true
  try {
    const body = { kind: form.kind, content: form.content.trim(), path: form.path.trim().replace(/^\/+/, '') }
    const key = keyOf(body.content, body.path)
    if (props.editing) await updateMemory(props.editing.id, props.editing.source === 'user' ? { ...body, key } : body)
    else await createMemory({ ...body, scope: props.scope, scope_id: props.scopeId, key, pinned: false })
    toast.success(props.editing ? '已保存' : '已创建')
    guard.reset()
    emit('saved')
    emit('close')
  } catch (e) {
    toast.error(props.editing ? '保存失败' : '创建失败', e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseDialog :open="open" :title="title" width="520px" @close="close">
    <form id="memory-form" class="form" novalidate v-on="v.form" @submit.prevent="submit">
      <div v-if="showKind" class="field">
        <span class="field-label">类型</span>
        <Select v-model="form.kind" :options="KINDS" label="类型" />
      </div>
      <label class="field">
        <span class="field-label required">内容</span>
        <textarea
          v-model="form.content"
          name="content"
          rows="3"
          maxlength="300"
          placeholder="例如：目录用中文命名"
          :aria-invalid="!!v.error('content')"
        />
        <span v-if="v.error('content')" class="field-error">{{ v.error('content') }}</span>
      </label>
      <label class="field">
        <span class="field-label">限定目录</span>
        <input v-model="form.path" name="path" placeholder="/照片/2023" />
      </label>
    </form>
    <template #footer>
      <button type="button" @click="close">取消</button>
      <button type="submit" form="memory-form" class="primary" :disabled="saving">
        {{ saving ? '保存中' : '保存' }}
      </button>
    </template>
  </BaseDialog>
</template>
