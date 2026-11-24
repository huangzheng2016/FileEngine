<script setup lang="ts">
import Alert from '@/base/Alert.vue'
import CopyText from '@/base/CopyText.vue'
import type { StorageFormApi } from '@/lib/useStorageForm'

// The result of the last connection attempt, as long as it failed.
const props = defineProps<{ s: StorageFormApi }>()
const { probe, hostKey, where, trustNewKey } = props.s
</script>

<template>
  <Alert
    v-if="probe.error && !probe.loading"
    tone="danger"
    :title="hostKey.changed ? '服务器指纹已变化' : `连接 ${where} 失败`"
  >
    {{ probe.error }}
    <p v-if="probe.detail" class="detail text-sm text-muted">{{ probe.detail }}</p>
    <p v-if="hostKey.changed" class="detail">
      如果你刚重装过服务器或换了机器，可以信任新指纹：<CopyText :value="hostKey.value" />
    </p>
    <template v-if="hostKey.changed" #action>
      <button type="button" class="btn-sm" @click="trustNewKey">信任新指纹</button>
    </template>
  </Alert>
</template>

<style scoped>
.detail {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: var(--space-1) 0 0;
  word-break: break-all;
}
</style>
