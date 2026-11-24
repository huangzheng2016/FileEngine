<script setup lang="ts">
import Alert from '@/base/Alert.vue'
import Select from '@/base/Select.vue'
import { PRESET_OPTIONS, type ModelFormApi } from '@/lib/useModelForm'

// Address and key of the provider. While adding, a failed model list offers typing the name by hand.
const props = defineProps<{ m: ModelFormApi; editing: boolean }>()
defineEmits<{ manual: [] }>()
const { form, preset, isCustom, keySaved, keyRequired, err, vk, catalog, changePreset } = props.m
</script>

<template>
  <div class="form-grid" v-on="vk.form">
    <div v-if="editing" class="field">
      <span class="field-label">服务商</span>
      <Select :model-value="form.preset" :options="PRESET_OPTIONS" label="服务商" @update:model-value="changePreset" />
    </div>
    <label class="field" :class="{ 'span-2': !editing }">
      <span class="field-label" :class="{ required: isCustom }">接口地址</span>
      <input
        v-model="form.base_url"
        name="base_url"
        class="mono"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        :placeholder="isCustom ? 'https://example.com/v1' : preset.baseUrl"
        :aria-invalid="!!err('base_url')"
      />
      <span v-if="err('base_url')" class="field-error">{{ err('base_url') }}</span>
      <span v-else-if="!editing && isCustom" class="field-hint">填服务商文档给的地址，通常以 /v1 结尾</span>
      <span v-else-if="!editing && form.preset === 'ollama'" class="field-hint">
        Ollama 在另一台电脑上时，把 127.0.0.1 换成那台电脑的 IP
      </span>
    </label>
    <label class="field span-2">
      <span class="field-label" :class="{ required: keyRequired }">API Key</span>
      <input
        v-model="form.api_key"
        name="api_key"
        type="password"
        autocomplete="new-password"
        :placeholder="keySaved ? '已保存，输入新的可替换' : preset.keyRequired ? '粘贴 API Key' : '不需要就留空'"
        :aria-invalid="!!err('api_key')"
      />
      <span v-if="err('api_key')" class="field-error">{{ err('api_key') }}</span>
      <span v-else-if="preset.keyUrl && !keySaved" class="field-hint">
        还没有？<a :href="preset.keyUrl" target="_blank" rel="noopener noreferrer">去 {{ preset.title }} 后台创建</a>
      </span>
    </label>
  </div>
  <Alert v-if="!editing && catalog.error && !catalog.loading" tone="danger" title="没能取到模型列表">
    {{ catalog.error }}
    <p v-if="catalog.detail" class="detail text-sm text-muted">{{ catalog.detail }}</p>
    <template #action>
      <button type="button" class="btn-sm" @click="$emit('manual')">手动填写模型名</button>
    </template>
  </Alert>
</template>

<style scoped>
.detail {
  margin: var(--space-1) 0 0;
  word-break: break-all;
}
</style>
