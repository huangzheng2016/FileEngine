<script setup lang="ts">
import BaseDialog from './BaseDialog.vue'
import { confirmState, resolveConfirm } from './useConfirm'
</script>

<template>
  <BaseDialog :open="confirmState.open" :title="confirmState.title" width="420px" @close="resolveConfirm(false)">
    <p class="confirm-message">{{ confirmState.message }}</p>
    <template #footer>
      <!-- Destructive confirms focus 取消 so a stray Enter does not delete. -->
      <button type="button" :autofocus="confirmState.danger" @click="resolveConfirm(false)">取消</button>
      <button
        type="button"
        :class="confirmState.danger ? 'danger' : 'primary'"
        :autofocus="!confirmState.danger"
        @click="resolveConfirm(true)"
      >
        {{ confirmState.confirmText }}
      </button>
    </template>
  </BaseDialog>
</template>

<style scoped>
.confirm-message {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  color: var(--text-secondary);
}
</style>
