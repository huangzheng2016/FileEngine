import { computed, onBeforeUnmount, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useConfirm } from './useConfirm'

// Asks before discarding unsaved edits. Call reset() whenever the form is loaded
// or saved; the guard compares against that snapshot.
//   dialogs: route every close through `if (await guard.confirmDiscard()) emit('close')`
//   pages:   useDirtyGuard(() => form, { route: true }) also covers navigation and tab close
export function useDirtyGuard(source: () => unknown, opts: { route?: boolean } = {}) {
  const confirm = useConfirm()
  const snapshot = ref(JSON.stringify(source()))
  const dirty = computed(() => JSON.stringify(source()) !== snapshot.value)

  function reset() {
    snapshot.value = JSON.stringify(source())
  }

  function confirmDiscard() {
    if (!dirty.value) return Promise.resolve(true)
    return confirm({
      title: '放弃修改',
      message: '有尚未保存的修改，离开后将丢失。',
      confirmText: '放弃修改',
      danger: true,
    })
  }

  if (opts.route) {
    onBeforeRouteLeave(() => confirmDiscard())
    const onUnload = (e: BeforeUnloadEvent) => {
      if (dirty.value) e.preventDefault()
    }
    window.addEventListener('beforeunload', onUnload)
    onBeforeUnmount(() => window.removeEventListener('beforeunload', onUnload))
  }

  return { dirty, reset, confirmDiscard }
}
