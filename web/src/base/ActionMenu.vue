<script setup lang="ts">
import { nextTick, ref } from 'vue'
import Icon from './Icon.vue'
import Popover from './Popover.vue'
import { vTip } from './tooltip'
import type { IconName } from './icons'

export interface MenuItem {
  label: string
  icon?: IconName
  danger?: boolean
  disabled?: boolean
  // shown as a tooltip; required when disabled so the user knows why
  title?: string
  onSelect: () => void
}

// Default trigger is the row "more" button. Pass label/icon for a labelled trigger
// (e.g. the user menu in the shell header).
withDefaults(defineProps<{ items: MenuItem[]; label?: string; icon?: IconName }>(), {
  label: '',
  icon: 'dots',
})

const open = ref(false)
const trigger = ref<HTMLButtonElement | null>(null)
const popover = ref<InstanceType<typeof Popover> | null>(null)

function menuItems() {
  return [...(popover.value?.panel?.querySelectorAll<HTMLElement>('[role="menuitem"]:not(:disabled)') ?? [])]
}

function move(step: number) {
  const items = menuItems()
  if (items.length === 0) return
  const i = items.indexOf(document.activeElement as HTMLElement)
  items[(i + step + items.length) % items.length].focus()
}

async function show() {
  open.value = true
  await nextTick()
  move(1)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    move(e.key === 'ArrowDown' ? 1 : -1)
  } else if (e.key === 'Tab') {
    open.value = false
  }
}

function select(item: MenuItem) {
  open.value = false
  trigger.value?.focus()
  item.onSelect()
}
</script>

<template>
  <button
    ref="trigger"
    type="button"
    :class="label ? 'ghost' : 'ghost btn-icon btn-sm'"
    :aria-label="label || '更多操作'"
    aria-haspopup="menu"
    :aria-expanded="open"
    @click.stop="open ? (open = false) : show()"
  >
    <Icon :name="icon" />
    <span v-if="label">{{ label }}</span>
  </button>
  <Popover ref="popover" :open="open" :anchor="trigger" :width="168" align="end" @close="open = false">
    <div role="menu" @keydown="onKeydown">
      <button
        v-for="item in items"
        :key="item.label"
        v-tip="item.title"
        type="button"
        role="menuitem"
        class="menu-item"
        :class="{ danger: item.danger }"
        :disabled="item.disabled"
        @click="select(item)"
      >
        <Icon v-if="item.icon" :name="item.icon" :size="14" />
        {{ item.label }}
      </button>
    </div>
  </Popover>
</template>

<style scoped>
.menu-item {
  width: 100%;
  height: 30px;
  justify-content: flex-start;
  padding: 0 8px;
  color: var(--text);
  background: transparent;
  border: 0;
}
.menu-item:hover:not(:disabled),
.menu-item:focus-visible {
  background: var(--surface-hover);
  outline: none;
}
.menu-item.danger {
  color: var(--danger);
}
.menu-item.danger:hover:not(:disabled),
.menu-item.danger:focus-visible {
  background: var(--danger-soft);
}
</style>
