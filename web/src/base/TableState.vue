<script setup lang="ts">
import EmptyState from './EmptyState.vue'
import Loading from './Loading.vue'

// The last row of every <tbody>. A failed request never renders as "no data",
// and a refresh keeps the existing rows on screen instead of blanking the table.
defineProps<{
  colspan: number
  loading: boolean
  error: string
  empty: boolean
  emptyText?: string
}>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <tr v-if="error && !loading">
    <td :colspan="colspan" class="state-cell">
      <span class="state-error">
        <span class="text-danger">加载失败：{{ error }}</span>
        <button type="button" class="btn-sm" @click="$emit('retry')">重试</button>
      </span>
    </td>
  </tr>
  <tr v-else-if="empty && loading">
    <td :colspan="colspan" class="state-cell">
      <Loading :delay="0" />
    </td>
  </tr>
  <tr v-else-if="empty">
    <td :colspan="colspan" class="state-cell">
      <EmptyState :title="emptyText ?? '暂无数据'">
        <slot />
      </EmptyState>
    </td>
  </tr>
</template>

<style scoped>
.state-error {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}
</style>
