/**
 * The view/emoji icon sprite (public/flow-view-icons.svg) is inlined into the page once, so glyphs reference
 * `#Name` in the same document. External `<use href="/flow-view-icons.svg#Name">` breaks for good when that one
 * request fails (the browser caches the failed reference until a full reload) and doesn't work cross-origin.
 */
const SPRITE_ID = 'flow-view-icon-sprite'
let loading: Promise<void> | null = null

export function ensureViewIconSprite(): Promise<void> {
  if (typeof document === 'undefined' || document.getElementById(SPRITE_ID)) return Promise.resolve()
  loading ??= fetch(`${import.meta.env.BASE_URL}flow-view-icons.svg`)
    .then(response => {
      if (!response.ok) throw new Error(`icon sprite ${response.status}`)
      return response.text()
    })
    .then(markup => {
      if (document.getElementById(SPRITE_ID)) return
      const host = document.createElement('div')
      host.id = SPRITE_ID
      host.setAttribute('aria-hidden', 'true')
      host.style.cssText = 'position:absolute;width:0;height:0;overflow:hidden'
      host.innerHTML = markup
      document.body.append(host) // after the inlined core sprites, so their ids (e.g. Project) win
    })
    // A failed load is retried by the next glyph that asks for it.
    .catch(() => { loading = null })
  return loading
}
