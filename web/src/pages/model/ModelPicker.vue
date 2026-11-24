<script setup lang="ts">
import Combobox from '@/base/Combobox.vue'
import Icon from '@/base/Icon.vue'
import { vTip } from '@/base/tooltip'
import type { ModelFormApi } from '@/lib/useModelForm'

// Any name can be typed; the provider's own list and the recommendations are suggestions.
const props = defineProps<{ m: ModelFormApi; editing: boolean }>()
const { picked, loadModelOptions, err, catalog, loadCatalog } = props.m
</script>

<template>
  <div class="field span-2">
    <span class="field-label required">模型</span>
    <div class="picker-row">
      <Combobox
        v-model="picked"
        :load="loadModelOptions"
        placeholder="选择或输入模型名称"
        :invalid="!!err('model')"
        class="picker-combo"
      />
      <button
        v-if="editing"
        v-tip="catalog.loading ? '正在获取' : ''"
        type="button"
        :disabled="catalog.loading"
        @click="loadCatalog"
      >
        <Icon name="refresh-cw" :size="14" />获取列表
      </button>
    </div>
    <span v-if="err('model')" class="field-error">{{ err('model') }}</span>
    <span v-else-if="editing && catalog.error && !catalog.loading" class="field-error">{{ catalog.error }}</span>
  </div>
</template>

<style scoped>
.picker-row {
  display: flex;
  gap: var(--space-2);
}
.picker-combo {
  flex: 1;
  min-width: 0;
}
</style>
