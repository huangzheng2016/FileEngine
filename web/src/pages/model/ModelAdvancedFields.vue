<script setup lang="ts">
import Combobox from '@/base/Combobox.vue'
import Select from '@/base/Select.vue'
import Switch from '@/base/Switch.vue'
import { effortOptions } from '@/lib/modelForm'
import { CONTEXT_OPTIONS, type ModelFormApi } from '@/lib/useModelForm'

const props = defineProps<{ m: ModelFormApi }>()
const { form, err, vm } = props.m
const loadEfforts = async (q: string) => effortOptions(q)
</script>

<template>
  <div class="form-grid" v-on="vm.form">
    <div class="field">
      <span class="field-label">上下文长度</span>
      <Select v-model="form.context" :options="CONTEXT_OPTIONS" label="上下文长度" />
      <span class="field-hint">按模型名称自动估计</span>
    </div>
    <label v-if="form.context === 'custom'" class="field">
      <span class="field-label">自定义长度</span>
      <input
        v-model="form.context_custom"
        name="context_custom"
        placeholder="300K"
        :aria-invalid="!!err('context_custom')"
      />
      <span v-if="err('context_custom')" class="field-error">{{ err('context_custom') }}</span>
      <span v-else class="field-hint">可写 300K、1.5M 或 token 数</span>
    </label>
    <label class="field">
      <span class="field-label">温度</span>
      <input
        v-model.number="form.temperature"
        name="temperature"
        type="number"
        min="0"
        max="2"
        step="0.1"
        :aria-invalid="!!err('temperature')"
      />
      <span v-if="err('temperature')" class="field-error">{{ err('temperature') }}</span>
      <span v-else class="field-hint">整理文件建议 0.3；填 0 用模型默认值</span>
    </label>
    <label class="field">
      <span class="field-label">单次最多输出</span>
      <input
        v-model.number="form.max_tokens"
        name="max_tokens"
        type="number"
        min="1"
        placeholder="不限"
        :aria-invalid="!!err('max_tokens')"
      />
      <span v-if="err('max_tokens')" class="field-error">{{ err('max_tokens') }}</span>
      <span v-else class="field-hint">留空用模型默认值</span>
    </label>
    <div class="field">
      <span class="field-label">能读图</span>
      <Switch v-model="form.vision_enabled" label="能读图" />
    </div>
    <div class="field">
      <span class="field-label">开启思考</span>
      <Switch v-model="form.thinking_enabled" label="开启思考" />
    </div>
    <div v-if="form.thinking_enabled" class="field">
      <span class="field-label">思考强度</span>
      <Combobox v-model="form.effort" :load="loadEfforts" placeholder="模型默认" :invalid="!!err('effort')" />
      <span v-if="err('effort')" class="field-error">{{ err('effort') }}</span>
    </div>
  </div>
</template>
