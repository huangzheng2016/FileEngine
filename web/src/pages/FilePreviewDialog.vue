<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import BaseDialog from '@/base/BaseDialog.vue'
import CodeBlock from '@/base/CodeBlock.vue'
import EmptyState from '@/base/EmptyState.vue'
import Loading from '@/base/Loading.vue'
import { errorMessage } from '@/base/errors'
import { fmtBytes } from '@/base/format'
import { fileContent } from '@/api/files'
import type { FileEntry } from '@/api/types'

const props = defineProps<{ file: FileEntry | null }>()
const emit = defineEmits<{ close: [] }>()

const IMAGE = /\.(jpe?g|png|gif|webp|bmp|svg)$/i
const TEXT = /\.(txt|md|json|ya?ml|xml|csv|log|ini|conf|toml|go|js|ts|py|sh|java|c|h|cpp|rs|html|css|sql)$/i
const MAX_TEXT = 512 * 1024

const kind = computed(() => {
  const f = props.file
  if (!f) return 'none'
  if (f.sensitive) return 'sensitive'
  if (IMAGE.test(f.name)) return 'image'
  if (TEXT.test(f.name) && f.size <= MAX_TEXT) return 'text'
  return 'none'
})

const loading = ref(false)
const error = ref('')
const url = ref('')
const text = ref('')
let ctrl: AbortController | null = null

function clear() {
  ctrl?.abort()
  if (url.value) URL.revokeObjectURL(url.value)
  url.value = ''
  text.value = ''
  error.value = ''
}

watch(
  () => props.file,
  async (f) => {
    clear()
    if (!f || (kind.value !== 'image' && kind.value !== 'text')) return
    const c = new AbortController()
    ctrl = c
    loading.value = true
    try {
      const blob = await fileContent(f.id, c.signal)
      if (kind.value === 'image') url.value = URL.createObjectURL(blob)
      else text.value = await blob.text()
    } catch (e) {
      if (!c.signal.aborted) error.value = errorMessage(e)
    } finally {
      if (ctrl === c) loading.value = false
    }
  },
)
onBeforeUnmount(clear)
</script>

<template>
  <BaseDialog :open="!!file" :title="file?.name ?? ''" width="760px" @close="emit('close')">
    <Loading v-if="loading" />
    <p v-else-if="error" class="text-danger">读取失败：{{ error }}</p>
    <img v-else-if="kind === 'image' && url" :src="url" :alt="file?.name" class="preview-img" />
    <CodeBlock v-else-if="kind === 'text'" :value="text" lang="text" :max-lines="200" />
    <EmptyState
      v-else
      icon="file"
      :title="kind === 'sensitive' ? '敏感文件不在这里打开' : '这种文件不能预览'"
      :hint="file ? fmtBytes(file.size) : ''"
    />
  </BaseDialog>
</template>

<style scoped>
.preview-img {
  display: block;
  max-width: 100%;
  max-height: 70vh;
  margin: 0 auto;
  object-fit: contain;
}
</style>
