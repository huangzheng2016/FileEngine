<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'
import Select from './Select.vue'
import { PAGE_SIZES } from './useList'

const props = defineProps<{ total: number; page: number; size: number }>()
const emit = defineEmits<{ 'update:page': [number]; 'update:size': [number] }>()

const SIZE_OPTIONS = PAGE_SIZES.map((s) => ({ value: s, label: `${s} 条/页` }))
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.size)))
</script>

<template>
  <nav v-if="total > 0" class="pager" aria-label="分页">
    <span class="text-muted tabular">共 {{ total }} 条</span>
    <span class="pager-spacer" />
    <Select
      :model-value="size"
      :options="SIZE_OPTIONS"
      label="每页条数"
      size="sm"
      @update:model-value="emit('update:size', $event)"
    />
    <button
      type="button"
      class="btn-icon"
      aria-label="上一页"
      :disabled="page <= 1"
      @click="emit('update:page', page - 1)"
    >
      <Icon name="chevron-left" />
    </button>
    <span class="tabular pager-pos">{{ page }} / {{ pages }}</span>
    <button
      type="button"
      class="btn-icon"
      aria-label="下一页"
      :disabled="page >= pages"
      @click="emit('update:page', page + 1)"
    >
      <Icon name="chevron-right" />
    </button>
  </nav>
</template>

<style scoped>
.pager {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-shrink: 0;
  font-size: var(--text-md);
}
.pager-spacer {
  flex: 1;
}
.pager-pos {
  min-width: 48px;
  text-align: center;
}
</style>
