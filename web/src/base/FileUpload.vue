<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from './Icon.vue'
import { fmtBytes } from './format'
import { toast } from './toast'

// Drop zone and file picker. It only selects and checks files; sending them is
// useUpload's job (or the page's, for files parsed in the browser such as CSV).
// Rejected files are reported with the reason, accepted ones emitted together.
const props = withDefaults(
  defineProps<{ accept?: string[]; maxBytes?: number; multiple?: boolean; disabled?: boolean; hint?: string }>(),
  { accept: () => [], maxBytes: 0, multiple: true, disabled: false, hint: '' },
)
const emit = defineEmits<{ select: [File[]] }>()

const input = ref<HTMLInputElement | null>(null)
const over = ref(false)
const acceptAttr = computed(() => props.accept.join(','))
const rules = computed(() => {
  const parts = []
  if (props.accept.length) parts.push(`支持 ${props.accept.join(' ')}`)
  if (props.maxBytes) parts.push(`单个文件不超过 ${fmtBytes(props.maxBytes)}`)
  return parts.join(',')
})

function reason(f: File): string {
  const ext = f.name.includes('.') ? f.name.slice(f.name.lastIndexOf('.')).toLowerCase() : ''
  if (props.accept.length && !props.accept.includes(ext)) return '不支持的文件类型'
  if (props.maxBytes && f.size > props.maxBytes) return `超过 ${fmtBytes(props.maxBytes)}`
  if (f.size === 0) return '文件为空'
  return ''
}

function take(list: FileList | null) {
  if (!list || props.disabled) return
  const files = props.multiple ? [...list] : [...list].slice(0, 1)
  const ok: File[] = []
  for (const f of files) {
    const why = reason(f)
    if (why) toast.error(`无法添加「${f.name}」`, why)
    else ok.push(f)
  }
  if (ok.length) emit('select', ok)
  if (input.value) input.value.value = ''
}

function onDrop(e: DragEvent) {
  over.value = false
  take(e.dataTransfer?.files ?? null)
}
</script>

<template>
  <div
    class="upload"
    :class="{ over, disabled }"
    role="button"
    :tabindex="disabled ? -1 : 0"
    :aria-disabled="disabled || undefined"
    @click="input?.click()"
    @keydown.enter.prevent="input?.click()"
    @keydown.space.prevent="input?.click()"
    @dragover.prevent="over = !disabled"
    @dragleave="over = false"
    @drop.prevent="onDrop"
  >
    <Icon name="upload" :size="20" class="upload-icon" />
    <span>拖拽文件到这里，或<span class="upload-link">点击选择</span></span>
    <span v-if="hint || rules" class="upload-hint">{{ hint || rules }}</span>
    <input
      ref="input"
      type="file"
      hidden
      :accept="acceptAttr"
      :multiple="multiple"
      @change="take(($event.target as HTMLInputElement).files)"
    />
  </div>
</template>

<style scoped>
.upload {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: var(--space-5) var(--space-4);
  font-size: var(--text-md);
  color: var(--text-secondary);
  text-align: center;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition:
    border-color var(--dur-fast) var(--ease),
    background-color var(--dur-fast) var(--ease);
}
.upload:hover,
.upload.over {
  border-color: var(--accent);
  background: var(--accent-soft);
}
.upload.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.upload-icon {
  color: var(--text-muted);
}
.upload-link {
  color: var(--accent-text);
}
.upload-hint {
  font-size: var(--text-sm);
  color: var(--text-muted);
}
</style>
