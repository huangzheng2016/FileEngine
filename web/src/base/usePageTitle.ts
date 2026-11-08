import { onBeforeUnmount, ref, watchEffect } from 'vue'

export const pageTitle = ref('')

// Detail pages name the object they show ("订单同步") instead of the route's
// static title. The shell renders it after the parent link from route meta.parent.
export function usePageTitle(title: () => string | undefined) {
  watchEffect(() => (pageTitle.value = title() ?? ''))
  onBeforeUnmount(() => (pageTitle.value = ''))
}
