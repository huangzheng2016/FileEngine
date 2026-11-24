<script setup lang="ts">
import { computed, watch } from 'vue'
import BaseDialog from '@/base/BaseDialog.vue'
import Steps from '@/base/Steps.vue'
import { vTip } from '@/base/tooltip'
import { useDirtyGuard } from '@/base/useDirtyGuard'
import type { Filesystem, Protocol } from '@/api/types'
import ChoiceGrid from '@/components/ChoiceGrid.vue'
import { PROTOCOLS, PROTOCOL_ORDER } from '@/lib/storage'
import { useStorageForm } from '@/lib/useStorageForm'
import StorageConnFields from './storage/StorageConnFields.vue'
import StorageFolderStep from './storage/StorageFolderStep.vue'
import StorageNameStep from './storage/StorageNameStep.vue'
import StorageProbeAlert from './storage/StorageProbeAlert.vue'

const props = defineProps<{ open: boolean; editing: Filesystem | null }>()
const emit = defineEmits<{ close: []; saved: [Filesystem] }>()

const s = useStorageForm(() => props.editing)
const { form, wizard, step, probe } = s
const guard = useDirtyGuard(() => s.form)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    s.init()
    guard.reset()
  },
)

const PROTOCOL_CHOICES = PROTOCOL_ORDER.map((p) => ({ value: p, title: PROTOCOLS[p].title, desc: PROTOCOLS[p].desc }))
const protocol = computed<Protocol>({ get: () => form.protocol, set: s.selectProtocol })

async function close() {
  s.browser.abort()
  if (await guard.confirmDiscard()) emit('close')
}

async function submit() {
  const saved = await s.submit()
  if (!saved) return
  guard.reset()
  emit('saved', saved)
  emit('close')
}

// Enter in a field moves the flow forward.
function onSubmit() {
  if (props.editing || step.value === 'name') void submit()
  else if (step.value === 'conn') void s.next()
}
</script>

<template>
  <BaseDialog :open="open" :title="editing ? '编辑存储' : '添加存储'" width="640px" @close="close">
    <form id="storage-form" class="form" novalidate @submit.prevent="onSubmit">
      <Steps
        v-if="!editing"
        :steps="wizard.steps"
        :index="wizard.index.value"
        :reached="wizard.reached.value"
        @go="wizard.goTo"
      />

      <ChoiceGrid v-if="!editing && step === 'type'" v-model="protocol" :options="PROTOCOL_CHOICES" label="连接方式" />

      <StorageNameStep v-if="editing || step === 'name'" :s="s" :editing="!!editing" />

      <StorageConnFields v-if="editing || step === 'conn'" :s="s" :editing="editing" />
      <StorageProbeAlert v-if="editing || step === 'conn' || step === 'dir'" :s="s" />

      <StorageFolderStep v-if="editing || step === 'dir'" :s="s" :editing="!!editing" />
    </form>

    <template #footer>
      <template v-if="editing">
        <button v-tip="probe.loading ? '正在测试' : ''" type="button" :disabled="probe.loading" @click="s.openBrowser">
          {{ probe.loading ? '测试中' : '测试连接' }}
        </button>
        <button type="button" @click="close">取消</button>
        <button
          v-tip="s.saving.value ? '正在保存' : ''"
          type="submit"
          form="storage-form"
          class="primary"
          :disabled="s.saving.value"
        >
          {{ s.saving.value ? '保存中' : '保存' }}
        </button>
      </template>
      <template v-else>
        <button type="button" @click="close">取消</button>
        <button v-if="!wizard.isFirst.value" type="button" @click="wizard.back">上一步</button>
        <button v-if="step === 'type'" type="button" class="primary" @click="s.next">下一步</button>
        <button
          v-else-if="step === 'conn'"
          v-tip="wizard.busy.value ? '正在连接' : ''"
          type="submit"
          form="storage-form"
          class="primary"
          :disabled="wizard.busy.value"
        >
          {{ wizard.busy.value ? '正在连接' : '连接' }}
        </button>
        <button
          v-else-if="step === 'dir'"
          v-tip="s.folderBlocked.value"
          type="button"
          class="primary"
          :disabled="!s.canUseFolder.value"
          @click="wizard.next"
        >
          使用这个文件夹
        </button>
        <button
          v-else
          v-tip="s.saving.value ? '正在保存' : ''"
          type="submit"
          form="storage-form"
          class="primary"
          :disabled="s.saving.value"
        >
          {{ s.saving.value ? '保存中' : '保存' }}
        </button>
      </template>
    </template>
  </BaseDialog>
</template>
