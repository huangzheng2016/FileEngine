<script setup lang="ts">
import ActionMenu, { type MenuItem } from '@/base/ActionMenu.vue'
import Icon from '@/base/Icon.vue'
import Steps from '@/base/Steps.vue'
import { FLOW_STEPS, type NextAction } from '@/lib/sessionView'

defineProps<{
  step: number
  running: boolean
  stopping: boolean
  busy: boolean
  next: NextAction
  menu: MenuItem[]
}>()
const emit = defineEmits<{ stop: []; next: [] }>()
</script>

<template>
  <div class="toolbar session-head">
    <Steps class="flow" :steps="FLOW_STEPS" :index="step" :reached="step" locked />
    <span class="head-actions">
      <button v-if="running" type="button" :disabled="stopping" @click="emit('stop')">
        <Icon name="stop" />{{ stopping ? '停止中' : '停止' }}
      </button>
      <button v-if="next.kind !== 'none'" type="button" class="primary" :disabled="busy" @click="emit('next')">
        <Icon :name="next.kind === 'review' ? 'eye' : 'play'" />{{ next.label }}
      </button>
      <ActionMenu :items="menu" />
    </span>
  </div>
</template>

<style scoped>
.session-head {
  flex-wrap: wrap;
  row-gap: var(--space-3);
}
.flow {
  flex: 1 1 360px;
  min-width: 0;
}
.head-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-left: auto;
}
</style>
