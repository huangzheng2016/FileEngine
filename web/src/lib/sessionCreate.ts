import { importCategories } from '@/api/files'
import { createRule } from '@/api/rules'
import { createSession, startTagging, type SessionInput } from '@/api/sessions'
import type { Session } from '@/api/types'
import type { TaskPreset } from '@/data/presets'

export interface CreateOutcome {
  session: Session
  // set when the session exists but a later step did not complete
  failed?: { step: 'template' | 'start'; cause: unknown }
}

// The session is created with auto_tag off: tagging starts only after the template's rules and categories exist,
// otherwise a quick scan could finish and tag without them.
export async function createTask(
  input: Omit<SessionInput, 'auto_tag'>,
  preset: TaskPreset | undefined,
  startNow: boolean,
): Promise<CreateOutcome> {
  const session = await createSession({ ...input, auto_tag: false })
  if (preset) {
    try {
      for (const [i, r] of preset.rules.entries()) await createRule(session.id, { ...r, position: i })
      if (preset.categories.length) await importCategories(input.filesystem_id, preset.categories)
    } catch (cause) {
      return { session, failed: { step: 'template', cause } }
    }
  }
  if (startNow) {
    try {
      await startTagging(session.id)
    } catch (cause) {
      return { session, failed: { step: 'start', cause } }
    }
  }
  return { session }
}
