// Field-level differences between two versions of a plain JSON value (config history,
// a save conflict's 服务端 vs 草稿). Objects are compared key by key; arrays and scalars
// compare as whole values, because "item 3 changed" is rarely what a person needs.

export type ChangeKind = 'added' | 'removed' | 'changed'

export interface Change {
  path: string[]
  kind: ChangeKind
  before: unknown
  after: unknown
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

const same = (a: unknown, b: unknown) => a === b || JSON.stringify(a) === JSON.stringify(b)

export function diffValues(before: unknown, after: unknown, path: string[] = []): Change[] {
  if (isObject(before) && isObject(after)) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])]
    return keys.flatMap((k) => {
      if (!(k in after)) return [{ path: [...path, k], kind: 'removed' as const, before: before[k], after: undefined }]
      if (!(k in before)) return [{ path: [...path, k], kind: 'added' as const, before: undefined, after: after[k] }]
      return diffValues(before[k], after[k], [...path, k])
    })
  }
  return same(before, after) ? [] : [{ path, kind: 'changed', before, after }]
}
