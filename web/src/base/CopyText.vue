<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import Icon from './Icon.vue'
import { toast } from './toast'
import { writeClipboard } from './clipboard'

// An id, key, or address the user is likely to paste elsewhere. The icon turns
// into a check for a moment instead of raising a toast.
// iconOnly: just the button, for places that already show the value (code blocks).
const props = withDefaults(defineProps<{ value: string; mono?: boolean; iconOnly?: boolean }>(), {
  mono: true,
  iconOnly: false,
})

const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  if (!(await writeClipboard(props.value))) return toast.error('复制失败：浏览器未允许访问剪贴板')
  copied.value = true
  clearTimeout(timer)
  timer = setTimeout(() => (copied.value = false), 1500)
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <span class="copy-text">
    <span v-if="!iconOnly" :class="{ mono }" class="copy-value truncate"
      ><slot>{{ value }}</slot></span
    >
    <button type="button" class="ghost btn-icon btn-sm" :aria-label="copied ? '已复制' : '复制'" @click.stop="copy">
      <Icon :name="copied ? 'check' : 'copy'" :size="14" :class="{ 'text-success': copied }" />
    </button>
  </span>
</template>

<style scoped>
.copy-text {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  max-width: 100%;
  min-width: 0;
}
.copy-value {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.copy-text button {
  margin: -4px 0;
}
</style>
