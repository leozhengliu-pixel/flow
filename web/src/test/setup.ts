import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

import { loadChineseDictionary } from '@/i18n/translate'

// The Chinese dictionary is a lazily loaded chunk in the app; tests translate synchronously.
await loadChineseDictionary()

Object.defineProperties(Element.prototype, {
  hasPointerCapture: { configurable: true, value: () => false },
  releasePointerCapture: { configurable: true, value: () => undefined },
  scrollIntoView: { configurable: true, value: () => undefined },
  setPointerCapture: { configurable: true, value: () => undefined },
})

// jsdom does not implement these and prints "Not implemented" to stderr; the code under test already copes with them being no-ops.
Object.defineProperties(Element.prototype, { scrollBy: { configurable: true, writable: true, value: () => undefined } })
Object.defineProperty(window, 'scrollBy', { configurable: true, writable: true, value: () => undefined })
// jsdom has no matchMedia; tests that care about a query define their own.
if (typeof window.matchMedia !== 'function') {
  Object.defineProperty(window, 'matchMedia', { configurable: true, writable: true, value: (query: string) => ({ matches: false, media: query, onchange: null, addEventListener: () => undefined, removeEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined, dispatchEvent: () => false }) })
}
Object.defineProperty(HTMLCanvasElement.prototype, 'getContext', { configurable: true, writable: true, value: () => null })
// Following a real link makes jsdom attempt a page navigation, which it cannot do and reports as "Not implemented". Stop it
// after every other listener has run, so tests can still tell whether the page left the click alone (`defaultPrevented`).
window.addEventListener('click', () => {
  window.addEventListener('click', event => {
    if (!event.defaultPrevented && event.target instanceof Element && event.target.closest('a[href]:not([href^="#"])')) event.preventDefault()
  }, { once: true })
}, true)

afterEach(cleanup)
