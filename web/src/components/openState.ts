import { inject, provide, reactive } from 'vue'

// Whether a folded step is open lives above the blocks: virtualized lanes
// unmount blocks that scroll out of view, and the reader's choice must outlive that.
const KEY = Symbol('open-state')

export type OpenState = Map<string, boolean>

export function provideOpenState(): OpenState {
  const state = reactive(new Map<string, boolean>()) as OpenState
  provide(KEY, state)
  return state
}

export function useOpenState(): OpenState {
  return inject<OpenState | null>(KEY, null) ?? (reactive(new Map<string, boolean>()) as OpenState)
}
