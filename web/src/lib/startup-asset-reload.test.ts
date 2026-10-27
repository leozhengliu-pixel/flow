import { readFileSync } from 'node:fs'
import path from 'node:path'
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

// The inline guard in index.html that reloads once when an entry file fails before the app runs.
const indexHtml = readFileSync(path.resolve(import.meta.dirname, '../../index.html'), 'utf8')
const guard = [...indexHtml.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(match => match[1]).find(source => source.includes("addEventListener('error'"))

describe('startup asset reload (index.html)', () => {
  const reload = vi.fn()
  const added: Array<[string, EventListenerOrEventListenerObject, boolean | AddEventListenerOptions | undefined]> = []

  beforeAll(() => {
    expect(guard).toBeTruthy()
    vi.stubGlobal('location', { ...window.location, reload })
    const original = window.addEventListener.bind(window)
    vi.spyOn(window, 'addEventListener').mockImplementation((type: string, listener: EventListenerOrEventListenerObject, options?: boolean | AddEventListenerOptions) => {
      added.push([type, listener, options])
      original(type, listener, options)
    })
    new Function(guard!)()
  })
  afterAll(() => {
    for (const [type, listener, options] of added) window.removeEventListener(type, listener, options)
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })
  beforeEach(() => {
    reload.mockReset()
    sessionStorage.clear()
    document.body.innerHTML = '<div id="root"><div class="flow-startup"></div></div>'
  })

  const fail = (element: HTMLElement) => {
    document.head.append(element)
    element.dispatchEvent(new Event('error'))
    element.remove()
  }
  const script = (src: string) => Object.assign(document.createElement('script'), { type: 'module', src })
  const stylesheet = (href: string) => Object.assign(document.createElement('link'), { rel: 'stylesheet', href })

  it('reloads once when the entry script or styles fail during startup', () => {
    fail(script('/assets/index-BBBB1111.js'))
    expect(reload).toHaveBeenCalledTimes(1)
    fail(stylesheet('/assets/index-BBBB3333.css'))
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('shares the loop guard with the in-app chunk reload', () => {
    sessionStorage.setItem('flow:stale-chunk-reload-at', String(Date.now()))
    fail(script('/assets/index-BBBB1111.js'))
    expect(reload).not.toHaveBeenCalled()
  })

  it('leaves failures after startup and other files to the app', () => {
    fail(script('https://example.com/widget.js'))
    document.body.innerHTML = '<div id="root"><main></main></div>'
    fail(script('/assets/route-BBBB2222.js'))
    expect(reload).not.toHaveBeenCalled()
  })
})
