// Recording stage for the feature tour.
//
// Playwright drives the app in real Chrome at 1440x900 CSS px with a 2x device
// scale factor. The page gets a prominent cursor, click ripples, focus rings,
// a spotlight and a title card (all injected, nothing in the app is changed).
// A CDP screencast captures 2880x1800 frames. The "camera" (smooth zoom/pan)
// and the captions are applied afterwards by ffmpeg from a log of keyframes, so
// the page itself always renders at 1x layout: popovers, menus and drag and
// drop behave exactly as in production, and a 2x zoom is still pixel-sharp.
import { createRequire } from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import { BASE, WORKSPACE, readState, repoRoot, sleep, tourDir } from './api.mjs'

const require = createRequire(path.join(repoRoot, 'web', 'package.json'))
const { chromium } = require('playwright-core')

export const VIEWPORT = { width: 1440, height: 900 }
const SCALE = Number(process.env.FLOW_TOUR_SCALE || 2)
const OUT_FPS = 30
export const mediaDir = path.join(repoRoot, 'docs', 'media')
const workRoot = path.join(tourDir, '.work')

const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2)
const now = () => Date.now() / 1000

// Injected into every page before the app runs.
const overlayScript = () => {
  const install = () => {
    if (document.getElementById('__tour') || !document.documentElement) return
    const root = document.createElement('div')
    root.id = '__tour'
    root.innerHTML = `
      <style>
        /* The list sizes the identifier column in "ch" of a narrower font, which clips two-digit numbers such as WEB-12. */
        html.__tour-on [role="list"] { --issue-identifier-width: 66px !important; }
        html.__tour-on, html.__tour-on body { overflow: hidden !important; }
        html.__tour-on, html.__tour-on * { cursor: none !important; }
        #__tour { position: fixed; inset: 0; pointer-events: none; z-index: 2147483000; font-family: var(--font-sans, 'Inter Variable', Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif); }
        #__tour-cursor { position: fixed; left: 0; top: 0; width: 34px; height: 34px; margin: -5px 0 0 -4px; z-index: 5; transition: transform 90ms ease-out; transform-origin: 4px 5px; filter: drop-shadow(0 3px 6px rgba(0,0,0,.55)); opacity: 0; }
        #__tour-cursor.down { transform: scale(.86); }
        #__tour-halo { position: fixed; left: 0; top: 0; width: 46px; height: 46px; margin: -23px 0 0 -23px; border-radius: 50%; background: radial-gradient(circle, rgba(124,134,255,.42), rgba(124,134,255,0) 70%); z-index: 4; opacity: 0; }
        .__tour-ripple { position: fixed; width: 18px; height: 18px; margin: -9px 0 0 -9px; border-radius: 50%; border: 3px solid rgba(124,134,255,.95); background: rgba(124,134,255,.28); animation: __tour-ripple 640ms ease-out forwards; z-index: 3; }
        @keyframes __tour-ripple { to { transform: scale(4.2); opacity: 0; } }
        .__tour-ring { position: fixed; border-radius: 9px; border: 2px solid rgba(124,134,255,.95); box-shadow: 0 0 0 4px rgba(124,134,255,.22), 0 0 24px rgba(124,134,255,.35); animation: __tour-ring 900ms ease-out forwards; z-index: 2; }
        @keyframes __tour-ring { 0% { opacity: 0; transform: scale(1.08); } 25% { opacity: 1; transform: scale(1); } 100% { opacity: 0; transform: scale(1); } }
        #__tour-spot { position: fixed; border-radius: 12px; box-shadow: 0 0 0 200vmax rgba(5,6,8,.62); opacity: 0; transition: opacity 450ms ease, left 450ms ease, top 450ms ease, width 450ms ease, height 450ms ease; z-index: 1; }
        #__tour-caption { position: fixed; left: 50%; bottom: 30px; transform: translate(-50%, 0); max-width: 78vw; padding: 12px 22px; border-radius: 999px; background: rgba(18,19,22,.92); color: #f4f5f8; font-size: 20px; line-height: 1.25; font-weight: 500; letter-spacing: -0.005em; border: 1px solid rgba(255,255,255,.14); box-shadow: 0 10px 40px rgba(0,0,0,.45); white-space: nowrap; display: none; z-index: 6; }
        #__tour-card { position: fixed; inset: 0; z-index: 9; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 14px; color: #f4f5f8; text-align: center; background: radial-gradient(1200px 700px at 50% 38%, #1c1e2a 0%, #0b0c10 70%); opacity: 1; transition: opacity 700ms ease; }
        #__tour-card.hide { opacity: 0; }
        #__tour-card .eyebrow { font-size: 15px; letter-spacing: .22em; text-transform: uppercase; color: #8b93ff; font-weight: 600; }
        #__tour-card .title { font-size: 60px; line-height: 1.08; font-weight: 650; letter-spacing: -0.03em; max-width: 1000px; }
        #__tour-card .sub { font-size: 24px; color: #a4a9bb; max-width: 880px; line-height: 1.4; }
        /* Used only to render caption stills with a transparent background. */
        html.__tour-render, html.__tour-render body { background: transparent !important; }
        html.__tour-render body > *:not(#__tour) { visibility: hidden !important; }
        html.__tour-render #__tour > *:not(#__tour-caption) { display: none !important; }
      </style>
      <div id="__tour-spot"></div>
      <div id="__tour-halo"></div>
      <svg id="__tour-cursor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M5 3.2l13.6 7.7-6.1 1.5 3.4 6.6-2.5 1.2-3.3-6.6L5 17.8z" fill="#fff" stroke="#0b0c10" stroke-width="1.5" stroke-linejoin="round"/></svg>
      <div id="__tour-caption"></div>
      <div id="__tour-card" style="display:none"></div>`
    document.documentElement.appendChild(root)
    document.documentElement.classList.add('__tour-on')
    const cursor = root.querySelector('#__tour-cursor')
    const halo = root.querySelector('#__tour-halo')
    const state = (window.__tour = window.__tour || { x: -100, y: -100 })
    const place = (x, y) => {
      state.x = x
      state.y = y
      cursor.style.left = halo.style.left = x + 'px'
      cursor.style.top = halo.style.top = y + 'px'
      cursor.style.opacity = halo.style.opacity = '1'
    }
    if (state.x > -50) place(state.x, state.y)
    addEventListener('mousemove', (event) => place(event.clientX, event.clientY), true)
    addEventListener(
      'mousedown',
      (event) => {
        cursor.classList.add('down')
        const ripple = document.createElement('div')
        ripple.className = '__tour-ripple'
        ripple.style.left = event.clientX + 'px'
        ripple.style.top = event.clientY + 'px'
        root.appendChild(ripple)
        setTimeout(() => ripple.remove(), 700)
      },
      true,
    )
    addEventListener('mouseup', () => cursor.classList.remove('down'), true)
  }
  install()
  if (!document.documentElement) document.addEventListener('DOMContentLoaded', install)
  new MutationObserver(install).observe(document, { childList: true, subtree: true })
}

const helperScript = () => {
  const tour = (window.__tour = window.__tour || { x: -100, y: -100 })
  tour.setSpot = (rect) => {
    const el = document.getElementById('__tour-spot')
    if (!el) return
    if (!rect) {
      el.style.opacity = '0'
      return
    }
    const pad = 8
    el.style.left = rect.x - pad + 'px'
    el.style.top = rect.y - pad + 'px'
    el.style.width = rect.w + pad * 2 + 'px'
    el.style.height = rect.h + pad * 2 + 'px'
    el.style.opacity = '1'
  }
  tour.ring = (rect) => {
    const root = document.getElementById('__tour')
    if (!root) return
    const ring = document.createElement('div')
    ring.className = '__tour-ring'
    const pad = 4
    ring.style.left = rect.x - pad + 'px'
    ring.style.top = rect.y - pad + 'px'
    ring.style.width = rect.w + pad * 2 + 'px'
    ring.style.height = rect.h + pad * 2 + 'px'
    root.appendChild(ring)
    setTimeout(() => ring.remove(), 1000)
  }
  tour.showCard = (eyebrow, title, sub) => {
    const card = document.getElementById('__tour-card')
    if (!card) return
    card.innerHTML = `<div class="eyebrow">${eyebrow}</div><div class="title">${title}</div>${sub ? `<div class="sub">${sub}</div>` : ''}`
    card.classList.remove('hide')
    card.style.display = 'flex'
  }
  tour.hideCard = () => {
    const card = document.getElementById('__tour-card')
    if (!card) return
    card.classList.add('hide')
    setTimeout(() => (card.style.display = 'none'), 760)
  }
}

export async function createStage({ name, startPath, title, subtitle, eyebrow = 'Flow', viewport = VIEWPORT, storage = {}, speed = 1 }) {
  const cookies = readState('cookies.json')
  if (!cookies?.maya) throw new Error('No demo session: run `node scripts/feature-tour/seed.mjs` first')
  const url = new URL(BASE)
  const browser = await chromium.launch({ channel: 'chrome', headless: true, args: ['--hide-scrollbars', '--force-color-profile=srgb'] })
  const context = await browser.newContext({
    viewport,
    deviceScaleFactor: 2880 / viewport.width,
    locale: 'en-US',
    colorScheme: 'dark',
    storageState: {
      cookies: [{ name: 'flow_clone_session', value: cookies.maya.split('=').slice(1).join('='), domain: url.hostname, path: '/', httpOnly: true, secure: false, sameSite: 'Lax', expires: Math.floor(Date.now() / 1000) + 86_400 * 7 }],
      origins: [
        {
          origin: url.origin,
          localStorage: [
            { name: 'flow:locale', value: 'en-US' },
            ...Object.entries(storage).map(([key, value]) => ({ name: key, value: typeof value === 'string' ? value : JSON.stringify(value) })),
          ],
        },
      ],
    },
  })
  await context.addInitScript(overlayScript)
  await context.addInitScript(helperScript)
  const sc = 2880 / viewport.width // capture scale: frames are always 2880 px wide
  const page = await context.newPage()
  const client = await context.newCDPSession(page)
  const outDir = path.join(workRoot, name)
  fs.rmSync(outDir, { recursive: true, force: true })
  fs.mkdirSync(outDir, { recursive: true })

  // Frame capture. The CDP screencast only delivers 1x frames, so a few workers
  // pull full 2x screenshots as fast as Chrome renders them (about 20-40 fps).
  const frames = []
  const writes = []
  let capturing = false
  let workers = []
  const captureWorker = async () => {
    while (capturing) {
      const started = now()
      let result
      try {
        result = await client.send('Page.captureScreenshot', { format: 'jpeg', quality: 90, optimizeForSpeed: true, clip: { x: 0, y: 0, width: viewport.width, height: viewport.height, scale: sc } })
      } catch {
        break
      }
      const index = frames.length
      const file = path.join(outDir, `f${String(index).padStart(6, '0')}.jpg`)
      frames.push({ file, t: (started + now()) / 2 })
      writes.push(fs.promises.writeFile(file, Buffer.from(result.data, 'base64')))
    }
  }

  // Camera and caption logs, in wall-clock seconds.
  const cameraSegments = []
  const captionEvents = []
  let cam = { cx: viewport.width / 2, cy: viewport.height / 2, s: 1 }
  let mouse = { x: viewport.width * 0.6, y: viewport.height * 0.55 }

  const api = {
    page,
    context,
    client,
    name,
    get mouse() {
      return mouse
    },

    async goto(pathname, { settle = 800 } = {}) {
      await page.goto(`${BASE}/${WORKSPACE}${pathname}`, { waitUntil: 'domcontentloaded' })
      await api.settled(settle)
    },
    async settled(ms = 600) {
      await page.waitForLoadState('networkidle', { timeout: 2500 }).catch(() => {})
      await page.evaluate(() => document.fonts?.ready)
      await sleep(ms)
    },
    async pause(ms) {
      await sleep(ms)
    },
    // Waits until the page text stops changing (a streamed answer has finished).
    async untilQuiet({ quiet = 2500, max = 90_000 } = {}) {
      const started = Date.now()
      let last = ''
      let since = Date.now()
      while (Date.now() - started < max) {
        const text = await page.evaluate(() => document.body.innerText)
        if (text !== last) {
          last = text
          since = Date.now()
        } else if (Date.now() - since >= quiet) return
        await sleep(300)
      }
      throw new Error('page never settled')
    },

    // -- recording ------------------------------------------------------------
    async begin() {
      await page.evaluate(([e, t, s]) => window.__tour.showCard(e, t, s), [eyebrow, title || '', subtitle || ''])
      await sleep(200)
      capturing = true
      workers = [captureWorker(), captureWorker()]
      await sleep(1500)
      await page.evaluate(() => window.__tour.hideCard())
      await sleep(750)
    },

    // -- cursor ---------------------------------------------------------------
    // Targets are Locators, CSS/Playwright selectors, or "t:Exact text".
    resolve(target) {
      if (typeof target !== 'string') return target
      return target.startsWith('t:') ? page.getByText(target.slice(2), { exact: true }) : page.locator(target)
    },
    async locate(target) {
      const locator = api.resolve(target).filter({ visible: true }).first()
      await locator.waitFor({ state: 'visible', timeout: 8000 })
      await locator.scrollIntoViewIfNeeded().catch(() => {})
      // The page itself never scrolls (inner panels do); screenshots are clipped to the page origin.
      await page.evaluate(() => { window.scrollTo(0, 0); document.documentElement.scrollTop = 0; document.body.scrollTop = 0 })
      return locator
    },
    async rectOf(target) {
      if (Array.isArray(target)) return { x: target[0], y: target[1], width: 0, height: 0 }
      const box = await (await api.locate(target)).boundingBox()
      if (!box) throw new Error(`no box for ${String(target)}`)
      return box
    },
    // `approach: 'horizontal'` slides sideways first (e.g. from a menu item into its submenu) before going to the target.
    async moveTo(target, { duration = 650, at = 'center', dx = 0, dy = 0, approach } = {}) {
      const box = await api.rectOf(target)
      const to = {
        x: box.x + (at === 'left' ? Math.min(28, box.width / 2) : at === 'right' ? box.width - Math.min(28, box.width / 2) : box.width / 2) + dx,
        y: box.y + box.height / 2 + dy,
      }
      if (approach === 'horizontal') await api.glide({ x: to.x, y: mouse.y }, Math.round(duration * 0.6))
      await api.glide(to, duration)
      return box
    },
    async glide(to, duration = 650) {
      const from = { ...mouse }
      const distance = Math.hypot(to.x - from.x, to.y - from.y)
      if (distance < 2) return
      const dur = Math.max(280, Math.min(duration + distance * 0.25, 1400))
      const steps = Math.max(14, Math.round(dur / 16))
      // A gentle arc so the motion reads as a hand, not a robot.
      const nx = -(to.y - from.y) / distance
      const ny = (to.x - from.x) / distance
      const bend = Math.min(60, distance * 0.08) * (to.x >= from.x ? 1 : -1)
      const started = Date.now()
      for (let i = 1; i <= steps; i++) {
        const t = ease(i / steps)
        const arc = Math.sin(Math.PI * t) * bend
        const x = from.x + (to.x - from.x) * t + nx * arc
        const y = from.y + (to.y - from.y) * t + ny * arc
        await page.mouse.move(x, y)
        mouse = { x, y }
        const wait = started + (dur * i) / steps - Date.now()
        if (wait > 0) await sleep(wait)
      }
      await page.mouse.move(to.x, to.y)
      mouse = { ...to }
    },
    // `expect` (a target) names something that must appear after the click; the
    // click is repeated when it does not (screenshots in flight can swallow one).
    async click(target, { ring = true, settle = 350, duration = 650, at = 'center', dx = 0, dy = 0, button = 'left', expect, approach } = {}) {
      const box = await api.moveTo(target, { duration, at, dx, dy, approach })
      if (ring && box.width > 0) await api.ring(box)
      for (let attempt = 0; attempt < 3; attempt++) {
        await sleep(140)
        await page.mouse.down({ button })
        await sleep(70)
        await page.mouse.up({ button })
        if (!expect) break
        const appeared = await api.resolve(expect).filter({ visible: true }).first().waitFor({ state: 'visible', timeout: 700 }).then(() => true, () => false)
        if (appeared) break
      }
      await sleep(settle)
    },
    async ring(box) {
      await page.evaluate((rect) => window.__tour.ring(rect), { x: box.x, y: box.y, w: box.width, h: box.height })
    },
    async hover(target, opts = {}) {
      await api.moveTo(target, opts)
    },
    async type(text, { delay = 48, jitter = 28 } = {}) {
      // Typing into the page without a focused text field would fire keyboard shortcuts.
      const editable = await page.evaluate(() => { const el = document.activeElement; return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA'].includes(el.tagName)) })
      if (!editable) throw new Error('refusing to type: no text field is focused')
      for (const char of text) {
        await page.keyboard.type(char)
        await sleep(delay + Math.random() * jitter)
      }
    },
    async press(key, { settle = 300 } = {}) {
      await page.keyboard.press(key)
      await sleep(settle)
    },
    async drag(from, to, { duration = 900, hold = 220 } = {}) {
      await api.moveTo(from, { duration: 600 })
      await sleep(160)
      await page.mouse.down()
      await sleep(hold)
      const target = await api.rectOf(to)
      await api.glide({ x: target.x + target.width / 2, y: target.y + Math.min(target.height / 2, 140) }, duration)
      await sleep(hold)
      await page.mouse.up()
      await sleep(400)
    },

    // -- camera (applied by ffmpeg afterwards) ----------------------------------
    // Frames the viewport rect (x, y, w, h) with a smooth eased zoom/pan.
    async focusRect(rect, { pad = 48, maxScale = 1.8, duration = 900 } = {}) {
      const vw = viewport.width
      const vh = viewport.height
      let s = Math.min((vw - pad * 2) / rect.w, (vh - pad * 2) / rect.h)
      s = Math.max(1, Math.min(s, maxScale, sc))
      let cx = rect.x + rect.w / 2
      let cy = rect.y + rect.h / 2
      cx = Math.min(vw - vw / (2 * s), Math.max(vw / (2 * s), cx))
      cy = Math.min(vh - vh / (2 * s), Math.max(vh / (2 * s), cy))
      const target = { cx, cy, s }
      const t0 = now()
      cameraSegments.push({ t0, t1: t0 + duration / 1000, from: { ...cam }, to: target })
      cam = target
      await sleep(duration)
    },
    async focus(target, opts = {}) {
      const box = await (await api.locate(target)).boundingBox()
      await api.focusRect({ x: box.x, y: box.y, w: box.width, h: box.height }, opts)
    },
    async unfocus(duration = 800) {
      const target = { cx: viewport.width / 2, cy: viewport.height / 2, s: 1 }
      const t0 = now()
      cameraSegments.push({ t0, t1: t0 + duration / 1000, from: { ...cam }, to: target })
      cam = target
      await sleep(duration)
    },
    async spot(target) {
      if (!target) return page.evaluate(() => window.__tour.setSpot(null))
      const box = await api.rectOf(target)
      await page.evaluate((rect) => window.__tour.setSpot(rect), { x: box.x, y: box.y, w: box.width, h: box.height })
    },
    async caption(text) {
      captionEvents.push({ t: now(), text: text || null })
    },

    // -- output ---------------------------------------------------------------
    async finish({ hold = 1200 } = {}) {
      await sleep(hold)
      const end = now()
      capturing = false
      await Promise.all(workers)
      await Promise.all(writes)
      frames.sort((a, b) => a.t - b.t)
      const captions = await renderCaptions(page, captionEvents, outDir)
      await context.close()
      await browser.close()
      return encode(name, outDir, frames, { cameraSegments, captions, end, viewport, speed })
    },
  }

  await page.goto(`${BASE}/${WORKSPACE}${startPath || ''}`, { waitUntil: 'domcontentloaded' })
  await api.settled(1200)
  return api
}

// One transparent PNG per distinct caption text, rendered with the app's font.
async function renderCaptions(page, events, outDir) {
  const spans = []
  for (let i = 0; i < events.length; i++) {
    if (!events[i].text) continue
    const next = events.slice(i + 1).find(() => true)
    spans.push({ text: events[i].text, t0: events[i].t, t1: next ? next.t : null })
  }
  const rendered = []
  await page.evaluate(() => {
    document.documentElement.classList.add('__tour-render')
    document.getElementById('__tour-caption').style.display = 'block'
  })
  for (const [index, span] of spans.entries()) {
    await page.evaluate((text) => {
      const el = document.getElementById('__tour-caption')
      el.textContent = text
      el.style.left = '40px'
      el.style.transform = 'none'
      el.style.bottom = '40px'
    }, span.text)
    const file = path.join(outDir, `caption-${index}.png`)
    await page.locator('#__tour-caption').screenshot({ path: file, omitBackground: true })
    rendered.push({ ...span, file })
  }
  return rendered
}

// ffmpeg expression for a value that eases between keyframe segments.
function piecewise(segments, read, initial) {
  const E = (u) => `if(lt(${u},0.5),4*pow(${u},3),1-pow(-2*${u}+2,3)/2)`
  let expression = String(segments.length ? read(segments.at(-1).to) : initial)
  for (let i = segments.length - 1; i >= 0; i--) {
    const seg = segments[i]
    const a = read(seg.from)
    const b = read(seg.to)
    const u = `((on/${OUT_FPS}-${seg.t0.toFixed(3)})/${(seg.t1 - seg.t0).toFixed(3)})`
    const lerp = `${a.toFixed(4)}+(${(b - a).toFixed(4)})*(${E(u)})`
    expression = `if(lt(on/${OUT_FPS},${seg.t0.toFixed(3)}),${a.toFixed(4)},if(lt(on/${OUT_FPS},${seg.t1.toFixed(3)}),${lerp},${expression}))`
  }
  return expression
}

function encode(name, outDir, frames, { cameraSegments, captions, end, viewport, speed }) {
  if (frames.length < 5) throw new Error(`only ${frames.length} frames captured`)
  // Clip time 0 is the first captured frame; frame and event times share the wall clock.
  const origin = frames[0].t
  const rel = (wall) => Math.max(0, wall - origin)
  // Concat list with per-frame durations (the screencast only emits frames when
  // something changes).
  const lines = []
  for (let i = 0; i < frames.length; i++) {
    const next = frames[i + 1]
    const duration = next ? Math.max(0.001, next.t - frames[i].t) : 0.5
    lines.push(`file '${path.basename(frames[i].file)}'`, `duration ${duration.toFixed(4)}`)
  }
  lines.push(`file '${path.basename(frames.at(-1).file)}'`)
  const list = path.join(outDir, 'frames.txt')
  fs.writeFileSync(list, lines.join('\n'))
  fs.mkdirSync(mediaDir, { recursive: true })
  const mp4 = path.join(mediaDir, `${name}.mp4`)
  const gif = path.join(mediaDir, `${name}.gif`)
  const ff = (args) => execFileSync('ffmpeg', ['-y', '-loglevel', 'error', ...args], { stdio: ['ignore', 'inherit', 'inherit'] })
  const total = frames.at(-1).t - frames[0].t
  console.log(`[${name}] ${frames.length} frames, ${total.toFixed(1)}s, ${cameraSegments.length} camera moves, ${captions.length} captions`)

  // Camera: eased zoom/pan between the logged keyframes, cropped from the 2x capture.
  const segments = cameraSegments.map((seg) => ({ ...seg, t0: rel(seg.t0), t1: Math.max(rel(seg.t0) + 0.05, rel(seg.t1)) }))
  const z = piecewise(segments, (c) => c.s, 1)
  const cx = piecewise(segments, (c) => c.cx, viewport.width / 2)
  const cy = piecewise(segments, (c) => c.cy, viewport.height / 2)
  const sc = 2880 / viewport.width
  const x = `${sc}*((${cx})-${viewport.width / 2}/(${z}))`
  const y = `${sc}*((${cy})-${viewport.height / 2}/(${z}))`
  const graph = []
  graph.push(`[0:v]fps=${OUT_FPS},zoompan=z='${z}':x='${x}':y='${y}':d=1:s=1440x900:fps=${OUT_FPS},format=yuv420p[v0]`)
  const inputs = ['-f', 'concat', '-safe', '0', '-i', list]
  let last = 'v0'
  captions.forEach((caption, index) => {
    const start = rel(caption.t0)
    const stop = caption.t1 ? rel(caption.t1) : total
    const length = Math.max(0.6, stop - start)
    inputs.push('-loop', '1', '-framerate', String(OUT_FPS), '-t', length.toFixed(3), '-i', caption.file)
    const input = index + 1
    graph.push(
      `[${input}:v]format=rgba,scale=iw*0.5:-1:flags=lanczos,fade=t=in:st=0:d=0.3:alpha=1,fade=t=out:st=${Math.max(0, length - 0.3).toFixed(3)}:d=0.3:alpha=1,setpts=PTS+${start.toFixed(3)}/TB[c${index}]`,
      `[${last}][c${index}]overlay=x=(W-w)/2:y=H-h-30:eof_action=pass:repeatlast=0[v${index + 1}]`,
    )
    last = `v${index + 1}`
  })
  // Optional global speed-up (applied last, so captions and camera stay in sync).
  if (speed !== 1) {
    graph.push(`[${last}]setpts=PTS/${speed}[vs]`)
    last = 'vs'
  }
  const script = path.join(outDir, 'graph.txt')
  fs.writeFileSync(script, graph.join(';\n'))
  ff([...inputs, '-/filter_complex', script, '-map', `[${last}]`, '-r', String(OUT_FPS), '-c:v', 'libx264', '-preset', 'slow', '-crf', '19', '-movflags', '+faststart', '-an', '-t', (total / speed).toFixed(2), mp4])
  if (process.env.FLOW_TOUR_SKIP_GIF) return { mp4, gif, seconds: total }
  // GIF: palette per clip, tuned down until it fits the size budget.
  const budget = Number(process.env.FLOW_TOUR_GIF_BUDGET || 5 * 1024 * 1024)
  const attempts = [
    { fps: 15, width: 1280, colors: 160, dither: 'bayer:bayer_scale=4' },
    { fps: 12, width: 1200, colors: 128, dither: 'bayer:bayer_scale=5' },
    { fps: 12, width: 1100, colors: 128, dither: 'none' },
    { fps: 10, width: 1100, colors: 112, dither: 'none' },
    { fps: 10, width: 1000, colors: 96, dither: 'none' },
    { fps: 8, width: 1000, colors: 96, dither: 'none' },
  ]
  for (const attempt of attempts) {
    const filters = `fps=${attempt.fps},scale=${attempt.width}:-1:flags=lanczos`
    const palette = path.join(outDir, 'palette.png')
    ff(['-i', mp4, '-vf', `${filters},palettegen=max_colors=${attempt.colors}:stats_mode=diff`, palette])
    ff(['-i', mp4, '-i', palette, '-lavfi', `${filters}[x];[x][1:v]paletteuse=dither=${attempt.dither}:diff_mode=rectangle`, '-loop', '0', gif])
    const size = fs.statSync(gif).size
    console.log(`[${name}] gif ${attempt.width}px ${attempt.fps}fps ${attempt.colors}c ${attempt.dither.split(':')[0]} -> ${(size / 1048576).toFixed(2)} MB`)
    if (size <= budget) break
  }
  return { mp4, gif, seconds: total }
}

export { sleep }
