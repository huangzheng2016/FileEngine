// Mobile keyboards and WebKit capitalise the first letter and autocorrect text
// fields by default, which mangles model names, hosts, paths and keys. The
// attributes are not inherited, so one capturing listener sets them on the field
// being entered; a field that declares its own value keeps it.
const TEXT_TYPES = new Set(['', 'text', 'search', 'email', 'url', 'tel', 'password', 'number'])

export function applyInputAttrs(el: Element | null) {
  if (!el) return
  const tag = el.tagName
  const textLike =
    tag === 'TEXTAREA' ||
    (tag === 'INPUT' && TEXT_TYPES.has((el.getAttribute('type') ?? '').trim().toLowerCase())) ||
    (el.getAttribute('contenteditable') ?? 'false') !== 'false'
  if (!textLike) return
  if (!el.hasAttribute('autocapitalize')) el.setAttribute('autocapitalize', 'off')
  if (!el.hasAttribute('autocorrect')) el.setAttribute('autocorrect', 'off')
}

export function installInputAttrs(doc: Document = document) {
  const on = (e: Event) => applyInputAttrs((e.composedPath()[0] as Element | undefined) ?? (e.target as Element))
  // pointerdown comes before focus, for keyboards that read the attributes early
  doc.addEventListener('focusin', on, true)
  doc.addEventListener('pointerdown', on, true)
}
