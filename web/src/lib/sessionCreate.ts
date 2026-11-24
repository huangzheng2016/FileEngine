import { createSession, type SessionInput } from '@/api/sessions'
import type { Session } from '@/api/types'
import type { TaskPreset } from '@/data/presets'

// One request: the server creates the session with the template's rules and categories in one transaction
// and, with auto_tag, starts tagging once the scan is complete.
export function createTask(
  input: Omit<SessionInput, 'auto_tag' | 'rules' | 'categories'>,
  preset: TaskPreset | undefined,
  startNow: boolean,
): Promise<Session> {
  return createSession({
    ...input,
    auto_tag: startNow,
    ...(preset && {
      rules: preset.rules.map((r, position) => ({ ...r, position })),
      categories: preset.categories.map((c) => ({ structure: '', ...c })),
    }),
  })
}
