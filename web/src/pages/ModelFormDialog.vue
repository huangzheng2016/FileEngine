<script setup lang="ts">
import { computed, watch } from 'vue'
import Alert from '@/base/Alert.vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Steps from '@/base/Steps.vue'
import { vTip } from '@/base/tooltip'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import type { ModelProvider } from '@/api/types'
import ChoiceGrid from '@/components/ChoiceGrid.vue'
import { MODEL_PRESETS } from '@/lib/modelForm'
import { useModelForm } from '@/lib/useModelForm'
import ModelAdvancedFields from './model/ModelAdvancedFields.vue'
import ModelConnectionFields from './model/ModelConnectionFields.vue'
import ModelPicker from './model/ModelPicker.vue'

const props = defineProps<{ open: boolean; editing: ModelProvider | null }>()
const emit = defineEmits<{ close: []; saved: [ModelProvider] }>()

const m = useModelForm(() => props.editing)
const { form, wizard, step } = m
const guard = useDirtyGuard(() => m.form)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    m.init()
    guard.reset()
  },
)

const PROVIDER_CHOICES = MODEL_PRESETS.map((p) => ({ value: p.id, title: p.title, desc: p.desc }))
const presetId = computed({ get: () => form.preset, set: m.selectPreset })

const showConnection = computed(() => !!props.editing || step.value === 'key')
const showModel = computed(() => !!props.editing || step.value === 'model')

async function close() {
  m.abort()
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  const saved = await m.submit()
  if (!saved) return
  guard.reset()
  emit('saved', saved)
  emit('close')
}

// Enter in a field moves the flow forward.
function onSubmit() {
  if (props.editing || step.value === 'model') void submit()
  else void wizard.next()
}
</script>

<template>
  <BaseDialog :open="open" :title="editing ? '编辑模型' : '添加模型'" width="640px" @close="close">
    <form id="model-form" class="form" novalidate @submit.prevent="onSubmit">
      <Steps
        v-if="!editing"
        :steps="wizard.steps"
        :index="wizard.index.value"
        :reached="wizard.reached.value"
        @go="wizard.goTo"
      />

      <ChoiceGrid
        v-if="!editing && step === 'provider'"
        v-model="presetId"
        :options="PROVIDER_CHOICES"
        label="服务商"
      />

      <ModelConnectionFields v-if="showConnection" :m="m" :editing="!!editing" @manual="m.typeModelByHand" />

      <div v-if="showModel" class="form-grid" v-on="m.vm.form">
        <ModelPicker :m="m" :editing="!!editing" />
        <label class="field span-2">
          <span class="field-label">名称</span>
          <input
            v-model="form.name"
            name="name"
            maxlength="64"
            placeholder="默认同模型名称"
            @input="m.nameTouched.value = true"
          />
        </label>
        <p class="span-2 text-sm text-muted summary">{{ m.summary.value }}</p>
      </div>

      <details
        v-if="showModel"
        :open="m.advanced.value"
        @toggle="m.advanced.value = ($event.target as HTMLDetailsElement).open"
      >
        <summary>高级设置</summary>
        <ModelAdvancedFields :m="m" class="advanced" />
      </details>

      <Alert
        v-if="showModel && m.testedNow.value"
        :tone="m.testedNow.value.ok ? 'success' : 'danger'"
        :title="m.testedNow.value.ok ? `${form.model} 可以用` : `${form.model} 没有通过测试`"
      >
        {{ m.testedNow.value.ok ? `回复：${m.testedNow.value.text}` : m.testedNow.value.text }}
        <p v-if="m.testedNow.value.detail" class="detail text-sm text-muted">{{ m.testedNow.value.detail }}</p>
      </Alert>
    </form>

    <template #footer>
      <button
        v-if="editing"
        v-tip="m.testing.value ? '正在测试' : ''"
        type="button"
        :disabled="m.testing.value"
        @click="m.runTest"
      >
        {{ m.testing.value ? '测试中' : '测试连接' }}
      </button>
      <button type="button" @click="close">取消</button>
      <button v-if="!editing && !wizard.isFirst.value" type="button" @click="wizard.back">上一步</button>
      <button
        v-if="!editing && step !== 'model'"
        v-tip="wizard.busy.value ? '正在获取模型' : ''"
        type="submit"
        form="model-form"
        class="primary"
        :disabled="wizard.busy.value"
      >
        {{ wizard.busy.value ? '正在获取模型' : '下一步' }}
      </button>
      <button
        v-else
        v-tip="m.saving.value || m.testing.value ? '正在处理' : ''"
        type="submit"
        form="model-form"
        class="primary"
        :disabled="m.saving.value || m.testing.value"
      >
        {{ m.saveLabel.value }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.summary {
  margin: 0;
}
.detail {
  margin: var(--space-1) 0 0;
  word-break: break-all;
}
.advanced {
  margin-top: var(--space-3);
}
</style>
