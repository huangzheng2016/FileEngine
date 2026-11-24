// @vitest-environment node
// The wire contract: types are generated from the IDL, every route has a client function, and the label
// maps cover exactly the enums the backend defines.
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import * as labels from '@/base/labels'
import { generate, IDL_DIR, loadIdl, OUT_FILE } from './idl-types.ts'

const ROOT = join(IDL_DIR, '..')
const SRC = join(fileURLToPath(new URL('..', import.meta.url)), 'src')
const read = (...p: string[]) => readFileSync(join(ROOT, ...p), 'utf8')

describe('IDL types', () => {
  it('idl.gen.ts is what the generator writes now (run `npm run gen:idl`)', async () => {
    expect(readFileSync(OUT_FILE, 'utf8')).toBe(await generate(IDL_DIR))
  })

  it('covers every IDL struct and field', () => {
    const { structs } = loadIdl(IDL_DIR)
    const out = readFileSync(OUT_FILE, 'utf8')
    expect(structs.length).toBeGreaterThan(50)
    for (const s of structs) {
      const fields = s.fields.filter((f) => f.name !== 'code' && f.name !== 'msg')
      if (!fields.length) {
        expect(out, s.name).toContain(`export type ${s.name} =`)
        continue
      }
      const block = new RegExp(`export interface ${s.name} \\{([\\s\\S]*?)\\n\\}`).exec(out)
      expect(block, `${s.name} is generated`).not.toBeNull()
      for (const f of fields) expect(block![1], `${s.name}.${f.name}`).toMatch(new RegExp(`\\n?\\s${f.name}\\??:`))
    }
  })

  it('types int64 ids with a string tag as Id and the rest as number', () => {
    const out = readFileSync(OUT_FILE, 'utf8')
    expect(out).toMatch(/export interface Session \{[\s\S]*?\bid: Id\b[\s\S]*?created_at: number/)
    expect(out).toMatch(/export interface SessionEvent \{\s*seq: number/)
    expect(out).not.toMatch(/\bcode: number\b/)
  })

  it('has a client call for every route except the raw redirect', () => {
    const { routes } = loadIdl(IDL_DIR)
    const dir = join(SRC, 'api')
    const source = readdirSync(dir)
      .filter((f) => f.endsWith('.ts') && !f.endsWith('.gen.ts'))
      .map((f) => readFileSync(join(dir, f), 'utf8'))
      .join('\n')
    expect(routes.length).toBeGreaterThan(60)
    const missing = routes.filter(
      (r) => r.path !== '/api/auth/bootstrap' && !new RegExp(`['\`]${r.path}['\`?]`).test(source),
    )
    expect(missing.map((r) => r.path)).toEqual([])
  })

  it('uses GET for reads and POST for writes only as the IDL says', () => {
    const { routes } = loadIdl(IDL_DIR)
    const dir = join(SRC, 'api')
    const source = readdirSync(dir)
      .filter((f) => f.endsWith('.ts') && !f.endsWith('.gen.ts'))
      .map((f) => readFileSync(join(dir, f), 'utf8'))
      .join('\n')
    const bad: string[] = []
    for (const r of routes) {
      // get('/api/x'...) / post('/api/x'...) / request('GET', '/api/x') / download('/api/x', { method })
      const post = new RegExp(`post(?:<[^(]*>)?\\(\\s*'${r.path}'|'POST',\\s*'${r.path}'`).test(source)
      const get = new RegExp(`get(?:<[^(]*>)?\\(\\s*'${r.path}'|'GET',\\s*'${r.path}'`).test(source)
      if (r.method === 'POST' && get) bad.push(`${r.path} is POST in the IDL`)
      if (r.method === 'GET' && post) bad.push(`${r.path} is GET in the IDL`)
    }
    expect(bad).toEqual([])
  })
})

// Go constants named like `<prefix>X = "value"` in a source file
function goConsts(file: string, name: RegExp): string[] {
  const out: string[] = []
  for (const m of read(file).matchAll(/^\s*([A-Z]\w*)\s*(?:\w+\s*)?=\s*"([^"]*)"/gm)) {
    if (name.test(m[1]) && m[2]) out.push(m[2])
  }
  return out
}

const keys = (o: object) => Object.keys(o).sort()

describe('labels follow the backend enums', () => {
  it.each([
    ['RUN_PHASE', labels.RUN_PHASE, 'internal/db/contract.go', /^RunPhase/],
    ['RUN_STATUS', labels.RUN_STATUS, 'internal/db/contract.go', /^Run(Running|Done|Partial|Error|Stopped)$/],
    ['PLAN_STATUS', labels.PLAN_STATUS, 'internal/db/contract.go', /^Plan(Draft|Approved)$/],
    ['MEMORY_SCOPE', labels.MEMORY_SCOPE, 'internal/db/contract.go', /^MemoryScope/],
    [
      'MEMORY_KIND',
      labels.MEMORY_KIND,
      'internal/db/contract.go',
      /^Memory(Preference|Convention|Correction|Fact|Note)$/,
    ],
    ['MEMORY_SOURCE', labels.MEMORY_SOURCE, 'internal/db/contract.go', /^MemorySource/],
    ['DATE_SOURCE', labels.DATE_SOURCE, 'internal/scanner/media.go', /^DateSource/],
    ['VALIDATION_ISSUE', labels.VALIDATION_ISSUE, 'internal/executor/executor.go', /^Issue[A-Z]/],
    ['OP_STATE', labels.OP_STATE, 'internal/db/exec_journal.go', /^Exec(Pending|Verified|Done|Failed|Reverted)$/],
  ])('%s', (_name, map, file, pattern) => {
    expect(keys(map)).toEqual(goConsts(file, pattern).sort())
  })

  it('ENTRY_ROLE has every decision role plus the undecided one', () => {
    const roles = goConsts('internal/db/contract.go', /^Role/)
    expect(keys(labels.ENTRY_ROLE)).toEqual([...roles, 'pending'].sort())
  })

  it('EXEC_MODE and its hints agree', () => {
    expect(keys(labels.EXEC_MODE)).toEqual(['copy', 'move'])
    expect(keys(labels.EXEC_MODE_HINT)).toEqual(keys(labels.EXEC_MODE))
  })

  it('says 助手 and 文件夹, never 目录 or a second name for the agent', () => {
    const text = JSON.stringify(labels)
    expect(text).not.toMatch(/目录|智能体|\bAI\b/)
  })
})
