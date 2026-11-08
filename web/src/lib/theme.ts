import { onScopeDispose, ref } from 'vue'

// The shell toggles `.dark` on <html>; components that need the theme as a value
// (third-party renderers) follow that class instead of keeping their own state.
export function useDarkClass() {
  const root = document.documentElement
  const dark = ref(root.classList.contains('dark'))
  const observer = new MutationObserver(() => (dark.value = root.classList.contains('dark')))
  observer.observe(root, { attributes: true, attributeFilter: ['class'] })
  onScopeDispose(() => observer.disconnect())
  return dark
}
