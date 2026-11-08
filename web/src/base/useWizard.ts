import { computed, ref } from 'vue'

export interface WizardStep<K extends string> {
  key: K
  label: string
  // runs when leaving forward; return false (after showing errors) to stay
  validate?: () => boolean | Promise<boolean>
}

// Multi-step flow state. Data lives in the page (one reactive object for all
// steps), so going back never loses it. Forward moves are gated by the current
// step's validate; backward moves and jumps to visited steps are free.
export function useWizard<K extends string>(steps: readonly WizardStep<K>[]) {
  const index = ref(0)
  const reached = ref(0)
  const busy = ref(false)

  const current = computed(() => steps[index.value])

  async function next() {
    if (busy.value || index.value >= steps.length - 1) return false
    busy.value = true
    try {
      const ok = (await current.value.validate?.()) ?? true
      if (!ok) return false
      index.value++
      reached.value = Math.max(reached.value, index.value)
      return true
    } finally {
      busy.value = false
    }
  }

  function back() {
    if (index.value > 0) index.value--
  }

  function goTo(i: number) {
    if (i <= reached.value && i >= 0) index.value = i
  }

  // after the final submit: forbid going back into a finished flow
  function finish() {
    index.value = steps.length - 1
    reached.value = index.value
  }

  function reset() {
    index.value = 0
    reached.value = 0
  }

  return {
    steps,
    index,
    reached,
    busy,
    current,
    isFirst: computed(() => index.value === 0),
    isLast: computed(() => index.value === steps.length - 1),
    next,
    back,
    goTo,
    finish,
    reset,
  }
}
