// Minimal highlighting for code blocks and editors: escape first, then wrap
// tokens, so the output is safe for v-html.

export type CodeLang = 'json' | 'sql' | 'text'

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

const JSON_TOKENS = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g

const SQL_KEYWORDS =
  'select|from|where|and|or|not|in|is|null|like|between|order|by|group|having|limit|offset|insert|into|values|update|set|delete|join|left|right|inner|outer|on|as|distinct|count|sum|avg|min|max|case|when|then|else|end|create|table|index|alter|drop|asc|desc|union|all|exists'
const SQL_TOKENS = new RegExp(`(--[^\\n]*)|('(?:''|[^'])*')|\\b(${SQL_KEYWORDS})\\b|\\b(\\d+(?:\\.\\d+)?)\\b`, 'gi')

export function highlight(code: string, lang: CodeLang): string {
  const safe = escape(code)
  if (lang === 'json') {
    return safe.replace(JSON_TOKENS, (m, str, colon, lit, num) => {
      if (str) return colon ? `<span class="tok-key">${str}</span>${colon}` : `<span class="tok-str">${str}</span>`
      if (lit) return `<span class="tok-lit">${lit}</span>`
      if (num) return `<span class="tok-num">${num}</span>`
      return m
    })
  }
  if (lang === 'sql') {
    return safe.replace(SQL_TOKENS, (m, comment, str, kw, num) => {
      if (comment) return `<span class="tok-comment">${comment}</span>`
      if (str) return `<span class="tok-str">${str}</span>`
      if (kw) return `<span class="tok-key">${kw}</span>`
      if (num) return `<span class="tok-num">${num}</span>`
      return m
    })
  }
  return safe
}

// '' when text is empty or a JSON object; otherwise the message to show.
export function jsonObjectError(text: string): string {
  if (!text.trim()) return ''
  try {
    const v = JSON.parse(text)
    return v && typeof v === 'object' && !Array.isArray(v) ? '' : '需要 JSON 对象，例如 {"key": "value"}'
  } catch {
    return 'JSON 格式错误，请检查引号、逗号和括号'
  }
}
