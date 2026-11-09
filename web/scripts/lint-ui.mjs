// Catches the UI mistakes that type checks and ESLint miss and that reviews keep finding:
// classes nobody defined (text runs together, toggles never look on), raw JSON in
// templates, units glued to values that may be '—', English codes in Chinese brackets,
// native <select> and title tooltips, half-width punctuation in Chinese text, and in project
// files: literal colors, raw z-index, restyled shared classes, local copies of labels.ts maps
// and formatting that bypasses base/format.ts.
// A line containing `lint-ui-ignore` is skipped.
// Tests find elements by role, label or text, not by style-less hook classes.
//
//   node scripts/lint-ui.mjs        (part of `pnpm check`)
import { readFileSync, readdirSync } from 'node:fs'
import { join, relative } from 'node:path'

const ROOT = new URL('..', import.meta.url).pathname
const SRC = join(ROOT, 'src')

function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? walk(join(dir, e.name)) : [join(dir, e.name)],
  )
}

const files = walk(SRC)
const vueFiles = files.filter((f) => f.endsWith('.vue'))
const cssFiles = files.filter((f) => f.endsWith('.css'))

// ---------- defined classes: global CSS plus every <style> block ----------

const defined = new Set()
const addSelectors = (css) => {
  for (const m of css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) defined.add(m[1])
}
for (const f of cssFiles) addSelectors(readFileSync(f, 'utf8'))
for (const f of vueFiles) {
  for (const m of readFileSync(f, 'utf8').matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)) addSelectors(m[1])
}

// ---------- checks ----------

const CJK = '\\u4e00-\\u9fff'
const problems = []
const report = (file, line, msg) => problems.push(`${relative(ROOT, file)}:${line}  ${msg}`)

// static class names, string literals in :class, and object keys in :class
function usedClasses(line) {
  const out = []
  for (const m of line.matchAll(/(?<![:\w-])class="([^"]*)"/g)) out.push(...m[1].split(/\s+/))
  for (const m of line.matchAll(/:class="([^"]*)"/g)) {
    // template literals build class names at runtime; their parts are not class names
    const expr = m[1].replace(/`[^`]*`/g, '')
    for (const s of expr.matchAll(/'([^'$]*)'/g)) out.push(...s[1].split(/\s+/))
    for (const k of expr.matchAll(/[{,]\s*([a-zA-Z][\w-]*)\s*:/g)) out.push(k[1])
  }
  return out.filter(Boolean)
}

const RULES = [
  [/\{\{[^}]*JSON\.stringify/, 'JSON.stringify in a template: show structured data with CodeBlock'],
  [
    /\}\}\s*(ms|%|[KMGT]?B)(?![\w])/,
    'unit after an interpolation renders "— ms" when empty: use fmtMs / fmtRatio / fmtBytes',
  ],
  [
    new RegExp(`[${CJK}]\\s*[（(]\\s*[a-z][a-z0-9]*([_.][a-z0-9]+)*\\s*[）)]`),
    'English code in brackets after Chinese text ("用户（actor.user）"): show the Chinese label only',
  ],
  [new RegExp(`[${CJK}][:(]|[:(][${CJK}]`), 'half-width ":" or "(" in Chinese text: use "：" and "（）"'],
  [
    // only value-then-word: an interpolation may start with Chinese punctuation (行{{ '，失败 2 行' }})
    new RegExp(`\\}\\}[${CJK}]`),
    'interpolation glued to the Chinese word after it ("{{ name }}保存于"): add a space',
  ],
]

// Whole tags, so attributes on their own lines are seen together with the tag name.
const TAG_RULES = [
  [(name) => name === 'select', 'native <select>: use Select (same list, hover and keyboard as the rest of the app)'],
  [
    (name, attrs) => /^[a-z]/.test(name) && /(^|\s)(:|v-bind:)?title=/.test(attrs),
    'title on an element: use v-tip (title is slow, unstyled, and never shows on a disabled button)',
  ],
]

for (const f of vueFiles) {
  const text = readFileSync(f, 'utf8')
  const tpl = /<template>([\s\S]*)<\/template>/.exec(text)
  const offset = tpl ? text.slice(0, tpl.index).split('\n').length - 1 : 0
  const body = tpl?.[1] ?? ''
  for (const m of body.matchAll(/<([a-zA-Z][\w-]*)((?:[^>"']|"[^"]*"|'[^']*')*)>/g)) {
    const line = body.slice(0, m.index).split('\n').length
    if (body.split('\n')[line - 1].includes('lint-ui-ignore')) continue
    for (const [test, msg] of TAG_RULES) if (test(m[1], m[2])) report(f, offset + line, msg)
  }
  ;(tpl?.[1] ?? '').split('\n').forEach((line, i) => {
    if (line.includes('lint-ui-ignore')) return
    const n = offset + i + 1
    for (const c of usedClasses(line))
      if (!defined.has(c)) report(f, n, `class "${c}" is not defined in styles/ or any <style>`)
    for (const [re, msg] of RULES) if (re.test(line)) report(f, n, msg)
  })
}

// Chinese string literals in scripts (toasts, labels, confirm text) get the punctuation rules too.
const PUNCT = RULES.slice(2, 4)
for (const f of files.filter((f) => /\.(ts|vue)$/.test(f) && !f.includes('/test/'))) {
  const text = readFileSync(f, 'utf8')
  const script = f.endsWith('.vue') ? (/<script[^>]*>([\s\S]*?)<\/script>/.exec(text)?.[1] ?? '') : text
  const offset = f.endsWith('.vue') ? text.slice(0, text.indexOf(script)).split('\n').length - 1 : 0
  script.split('\n').forEach((line, i) => {
    if (line.includes('lint-ui-ignore') || /^\s*(\/\/|\*)/.test(line)) return
    for (const lit of line.matchAll(/'([^'\n]*)'|`([^`\n]*)`/g)) {
      const s = lit[1] ?? lit[2]
      for (const [re, msg] of PUNCT) if (re.test(s)) report(f, offset + i + 1, msg)
    }
  })
}

// ---------- project files: everything outside the verbatim base/ and styles/ ----------

const isProject = (f) => {
  const r = relative(SRC, f)
  return !r.startsWith('base/') && !r.startsWith('styles/') && !r.startsWith('test/')
}
const lineAt = (text, index) => text.slice(0, index).split('\n').length
const ignored = (text, index) => text.split('\n')[lineAt(text, index) - 1].includes('lint-ui-ignore')

// Classes the shared stylesheets define; a page may use them but not restyle them.
const shared = new Set()
for (const f of cssFiles.filter((f) => relative(SRC, f).startsWith('styles/')))
  for (const m of readFileSync(f, 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .matchAll(/\.(-?[_a-zA-Z][\w-]*)/g))
    shared.add(m[1])

const STYLE_RULES = [
  [/#[0-9a-fA-F]{3,8}\b(?![\w-])/, 'hex color in a page style: use a color token (var(--…))'],
  [/\b(rgba?|hsla?)\(\s*\d/, 'literal color in a page style: use a color token (var(--…))'],
  [/z-index\s*:\s*-?\d/, 'raw z-index in a page style: use a --z-* token'],
]

for (const f of vueFiles.filter(isProject)) {
  const text = readFileSync(f, 'utf8')
  for (const m of text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)) {
    const start = m.index + m[0].indexOf(m[1])
    const css = m[1].replace(/\/\*[\s\S]*?\*\//g, (c) => c.replace(/[^\n]/g, ' '))
    css.split('\n').forEach((line, i) => {
      if (line.includes('lint-ui-ignore')) return
      const n = lineAt(text, start) + i
      for (const [re, msg] of STYLE_RULES) if (re.test(line)) report(f, n, msg)
    })
    // a selector whose subject is only a shared class (or a bare button) restyles it for this page
    for (const r of css.matchAll(/([^{};]+)\{/g)) {
      const head = r[1].trim()
      if (!head || head.startsWith('@') || /^(from|to|\d+%)/.test(head)) continue
      if (ignored(text, start + r.index)) continue
      for (const sel of head.split(',')) {
        const subject = sel
          .replace(/:(deep|global|slotted)\(([^)]*)\)/g, ' $2')
          .trim()
          .split(/[\s>+~]+/)
          .pop()
          .replace(/::?[\w-]+(\([^)]*\))?/g, '')
        const classes = [...subject.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)].map((c) => c[1])
        const tag = /^[a-z]+/.exec(subject)?.[0]
        if (tag === 'button' || (!tag && classes.length && classes.every((c) => shared.has(c))))
          report(
            f,
            lineAt(text, start + r.index + r[0].search(/\S/)),
            `page style restyles shared "${sel.trim()}": use a local class`,
          )
      }
    }
  }
}

// Label maps: the keys of every map in labels.ts.
const labelsFile = join(SRC, 'base', 'labels.ts')
const labelKeys = new Map()
for (const m of readFileSync(labelsFile, 'utf8').matchAll(/export const (\w+)[^=]*=\s*\{([\s\S]*?)\n\}/g))
  labelKeys.set(m[1], new Set([...m[2].matchAll(/^\s*'?([\w-]+)'?\s*:/gm)].map((k) => k[1])))
// the smallest map holding every value, so a subset of RULE_ROLE is not reported as ENTRY_ROLE
const labelsFor = (values) =>
  values.length < 2
    ? undefined
    : [...labelKeys]
        .filter(([, keys]) => values.every((v) => keys.has(v)))
        .sort((x, y) => x[1].size - y[1].size)[0]?.[0]

const SCRIPT_RULES = [
  [
    /\.(toLocaleString|toLocaleDateString|toLocaleTimeString|toFixed)\(/,
    'number/time formatting outside base/format.ts and base/time.ts: use fmtNumber / fmtRatio / the time helpers',
  ],
  [/\b[A-Z][A-Z0-9_]{2,}\[[^\]]+\]\??\.text\b/, 'direct label map access: use fmtLabel(MAP, value) or StatusPill'],
]

for (const f of files.filter((f) => /\.(ts|vue)$/.test(f) && isProject(f))) {
  const text = readFileSync(f, 'utf8')
  text.split('\n').forEach((line, i) => {
    if (line.includes('lint-ui-ignore') || /^\s*(\/\/|\*)/.test(line)) return
    for (const [re, msg] of SCRIPT_RULES) if (re.test(line)) report(f, i + 1, msg)
  })
  // [{ value: 'a', label: '…' }, …] or { a: '中文', … } named *LABEL* / *TEXT* / *NAME* / *TITLE* whose keys all belong to one labels.ts map
  for (const m of text.matchAll(/\[((?:\s*\{\s*value:\s*'[\w-]+'[^{}]*\},?)+)\s*\]/g)) {
    const values = [...m[1].matchAll(/value:\s*'([\w-]+)'/g)].map((v) => v[1])
    const map = labelsFor(values)
    if (map && !ignored(text, m.index))
      report(f, lineAt(text, m.index), `local options copy ${map}: use labelOptions(${map})`)
  }
  for (const m of text.matchAll(
    /\b\w*(?:LABEL|TEXT|NAME|TITLE)\w*\b[^=\n]*=\s*\{((?:\s*'?[\w-]+'?\s*:\s*'[^'\n]*[一-鿿][^'\n]*'\s*,?)+)\s*\}/g,
  )) {
    const keys = [...m[1].matchAll(/'?([\w-]+)'?\s*:\s*'/g)].map((k) => k[1])
    const map = labelsFor(keys)
    if (map && !ignored(text, m.index))
      report(f, lineAt(text, m.index), `local label map copies ${map}: use fmtLabel(${map}, …)`)
  }
}

if (problems.length) {
  console.error(problems.join('\n'))
  console.error(`\nlint-ui: ${problems.length} problem(s)`)
  process.exit(1)
}
console.log('lint-ui: ok')
