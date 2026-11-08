<script setup lang="ts">
// Memory tab of a session: what the agent keeps for this storage, and the notes the
// workers shared during this task.
import { ref } from 'vue'
import Icon from '@/base/Icon.vue'
import MemoryTable from './MemoryTable.vue'

defineProps<{ sessionId: number; filesystemId: number; rev: number }>()

const storageTable = ref<InstanceType<typeof MemoryTable> | null>(null)
const notes = ref(0)
</script>

<template>
  <div class="page">
    <section class="card">
      <div class="card-head">
        <h3>这个存储的记忆</h3>
        <button type="button" class="btn-sm" @click="storageTable?.create()">
          <Icon name="plus" :size="14" />添加
        </button>
      </div>
      <MemoryTable ref="storageTable" scope="filesystem" :scope-id="filesystemId" :rev="rev" />
    </section>
    <section v-show="notes > 0" class="card">
      <div class="card-head"><h3>本次任务的笔记</h3></div>
      <MemoryTable scope="session" :scope-id="sessionId" :rev="rev" :editable="false" @loaded="notes = $event" />
    </section>
  </div>
</template>
