<script setup lang="ts">
import { computed } from 'vue'
import Icon from './Icon.vue'

// A sortable column header. sort is useList's sort string: 'field' ascending,
// '-field' descending, '' the list's default order. Clicks cycle asc, desc, default.
const props = defineProps<{ field: string; sort: string; num?: boolean }>()
const emit = defineEmits<{ sort: [string] }>()

const dir = computed(() => (props.sort === props.field ? 'asc' : props.sort === `-${props.field}` ? 'desc' : ''))

function next() {
  emit('sort', dir.value === '' ? props.field : dir.value === 'asc' ? `-${props.field}` : '')
}
</script>

<template>
  <th :class="{ num }" :aria-sort="dir === 'asc' ? 'ascending' : dir === 'desc' ? 'descending' : 'none'">
    <button type="button" class="sort-btn" :class="{ active: dir }" @click="next">
      <slot />
      <Icon :name="dir === 'asc' ? 'arrow-up' : 'arrow-down'" :size="12" class="sort-icon" />
    </button>
  </th>
</template>

<style scoped>
.sort-btn {
  height: auto;
  gap: 4px;
  padding: 0;
  font: inherit;
  color: inherit;
  background: transparent;
  border: 0;
}
.sort-btn:hover,
.sort-btn.active {
  color: var(--text);
  background: transparent;
}
.sort-icon {
  opacity: 0;
}
.sort-btn:hover .sort-icon {
  opacity: 0.5;
}
.sort-btn.active .sort-icon {
  opacity: 1;
}
</style>
