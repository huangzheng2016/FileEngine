// Catches the UI mistakes that type checks and ESLint miss and that reviews keep finding:
// classes nobody defined (text runs together, toggles never look on), raw JSON in
// templates, units glued to values that may be '—', English codes in Chinese brackets,
// native <select> and title tooltips, and half-width punctuation in Chinese text.
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

if (problems.length) {
  console.error(problems.join('\n'))
  console.error(`\nlint-ui: ${problems.length} problem(s)`)
  process.exit(1)
}
console.log('lint-ui: ok')
