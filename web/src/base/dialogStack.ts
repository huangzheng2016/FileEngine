// Every floating layer registers here so Esc closes only the topmost one,
// and body scroll stays locked while any modal is open.
const stack: symbol[] = []
let locks = 0

// Returns the layer's depth. Modals add it to their z-index so the one opened last
// is on top regardless of where it sits in the DOM (a confirm over a dialog).
export function pushLayer(id: symbol): number {
  removeLayer(id)
  return stack.push(id)
}

export function removeLayer(id: symbol) {
  const i = stack.indexOf(id)
  if (i >= 0) stack.splice(i, 1)
}

export function isTopLayer(id: symbol): boolean {
  return stack[stack.length - 1] === id
}

export function lockScroll() {
  if (locks++ === 0) document.body.classList.add('scroll-locked')
}

export function unlockScroll() {
  locks = Math.max(0, locks - 1)
  if (locks === 0) document.body.classList.remove('scroll-locked')
}
