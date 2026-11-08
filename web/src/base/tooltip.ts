import type { Directive } from 'vue'

// Tooltips. One floating label for the whole app, installed once in main.ts.
// Sources, in order:
//   v-tip="text"               any element: a disabled button's reason, a time's full value
//   .btn-icon[aria-label]      icon-only buttons show their label with no extra markup
//   .truncate that overflows   shows its full text
// Pages do not use the title attribute: it appears after a second, looks foreign, and never
// shows on a disabled button. Opens after a short hover, at once on keyboard focus, and at
// once while moving along a row of tipped controls. Touch never opens it, so a tip must
// never be the only way to learn something essential.
//   <button :disabled="!!blocked" v-tip="blocked">运行</button>
//   <a v-tip.right="item.label">…</a>     (side: top by default, .bottom / .right)

type Side = 'top' | 'bottom' | 'right'

const texts = new WeakMap<Element, string>()
const sides = new WeakMap<Element, Side>()

const OPEN_DELAY = 450
const WARM_MS = 400
const GAP = 6
const MARGIN = 8

let tip: HTMLDivElement | null = null
let current: HTMLElement | null = null
let timer: ReturnType<typeof setTimeout> | undefined
let hiddenAt = 0

function textFor(el: Element): string {
  const own = texts.get(el)
  if (own) return own
  if (el.matches('.btn-icon[aria-label]')) return el.getAttribute('aria-label') ?? ''
  if (el.matches('.truncate') && el.scrollWidth > el.clientWidth) return el.textContent?.trim() ?? ''
  return ''
}

function anchorOf(target: EventTarget | null): HTMLElement | null {
  for (let el = target instanceof Element ? target : null; el && el !== document.body; el = el.parentElement) {
    if (textFor(el)) return el as HTMLElement
  }
  return null
}

function place(el: HTMLElement, side: Side) {
  if (!tip) return
  const a = el.getBoundingClientRect()
  const w = tip.offsetWidth
  const h = tip.offsetHeight
  let top: number
  let left: number
  if (side === 'right') {
    top = a.top + a.height / 2 - h / 2
    left = a.right + GAP
  } else {
    const above = a.top - GAP - h
    const useTop = side === 'top' ? above >= MARGIN : a.bottom + GAP + h > window.innerHeight - MARGIN
    top = useTop ? above : a.bottom + GAP
    left = a.left + a.width / 2 - w / 2
  }
  tip.style.top = `${Math.max(MARGIN, Math.min(top, window.innerHeight - h - MARGIN))}px`
  tip.style.left = `${Math.max(MARGIN, Math.min(left, window.innerWidth - w - MARGIN))}px`
}

function show(el: HTMLElement) {
  clearTimeout(timer)
  const text = textFor(el)
  if (!text || !el.isConnected) return hide()
  if (!tip?.isConnected) {
    tip = document.createElement('div')
    tip.className = 'tooltip'
    tip.setAttribute('role', 'tooltip')
    tip.id = 'app-tooltip'
    document.body.appendChild(tip)
  }
  current?.removeAttribute('aria-describedby')
  current = el
  tip.textContent = text
  tip.hidden = false
  // icon buttons already announce the same text as their name
  if (texts.has(el)) el.setAttribute('aria-describedby', tip.id)
  place(el, sides.get(el) ?? 'top')
}

function hide() {
  clearTimeout(timer)
  if (!current) return
  current.removeAttribute('aria-describedby')
  current = null
  hiddenAt = Date.now()
  if (tip) tip.hidden = true
}

function schedule(el: HTMLElement) {
  clearTimeout(timer)
  if (current) hide()
  // moving along a toolbar: the next tip follows without waiting again
  const warm = Date.now() - hiddenAt < WARM_MS
  timer = setTimeout(() => show(el), warm ? 0 : OPEN_DELAY)
}

function onPointerOver(e: PointerEvent) {
  if (e.pointerType && e.pointerType !== 'mouse') return
  const el = anchorOf(e.target)
  if (el === current && el) return
  if (el) schedule(el)
  else hide()
}

function onPointerOut(e: PointerEvent) {
  if (!e.relatedTarget) hide()
}

function onFocusIn(e: FocusEvent) {
  const el = anchorOf(e.target)
  if (el && (e.target as Element).matches(':focus-visible')) show(el)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') hide()
}

let installed = false

export function installTooltips() {
  if (installed) return
  installed = true
  document.addEventListener('pointerover', onPointerOver, true)
  document.addEventListener('pointerout', onPointerOut, true)
  document.addEventListener('pointerdown', hide, true)
  document.addEventListener('focusin', onFocusIn, true)
  document.addEventListener('focusout', hide, true)
  document.addEventListener('keydown', onKeydown, true)
  window.addEventListener('scroll', hide, true)
  window.addEventListener('blur', hide)
}

function bind(el: HTMLElement, value: unknown, modifiers: Partial<Record<string, boolean>>) {
  if (typeof value === 'string' && value) texts.set(el, value)
  else texts.delete(el)
  sides.set(el, modifiers.right ? 'right' : modifiers.bottom ? 'bottom' : 'top')
  if (current === el) {
    if (textFor(el)) show(el)
    else hide()
  }
}

// Falsy values mean "no tip", so `v-tip="blocked"` works with '' / undefined.
export const vTip: Directive<HTMLElement, string | false | null | undefined> = {
  mounted: (el, b) => bind(el, b.value, b.modifiers),
  updated: (el, b) => bind(el, b.value, b.modifiers),
  beforeUnmount(el) {
    texts.delete(el)
    if (current === el) hide()
  },
}
