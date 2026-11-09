#!/usr/bin/env node
// Renders Flow's welcome-notification intro video (public/media/flow-intro.{mp4,webm}, poster, captions).
//
// It drives Flow's own UI with Playwright against a private dev stack seeded with demo data, captures the page with
// CDP screencast frames at 1600x900 (1280x720 CSS px at a 1.25 device scale), and stitches them with ffmpeg.
//
// Prerequisites (all private to you; never run this against a shared workspace):
//   - ffmpeg on PATH
//   - the Flow API running with an empty database, auth disabled and the agent enabled against the demo model server this
//     script starts:  FLOW_AUTH_DISABLED=true FLOW_AGENT_ENABLED=true FLOW_AGENT_PROTOCOL=openai-responses
//                      FLOW_AGENT_BASE_URL=http://127.0.0.1:$FLOW_INTRO_MODEL_PORT FLOW_AGENT_MODEL=demo
//                      FLOW_AGENT_TOOLS_ENABLED=true FLOW_AGENT_WRITE_TOOLS=true
//   - the web dev server proxying to that API
//
// Env: FLOW_INTRO_WEB (default http://127.0.0.1:4173), FLOW_INTRO_API (default http://127.0.0.1:4180),
//      FLOW_INTRO_MODEL_PORT (default 4390), FLOW_INTRO_OUT (default public/media), FLOW_INTRO_FRAMES (frame scratch dir).
// Usage: node scripts/render-intro-video.mjs
import { spawnSync } from 'node:child_process'
import { mkdirSync, rmSync, writeFileSync, statSync, readdirSync } from 'node:fs'
import http from 'node:http'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from '@playwright/test'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const WEB = process.env.FLOW_INTRO_WEB ?? 'http://127.0.0.1:4173'
const API = process.env.FLOW_INTRO_API ?? 'http://127.0.0.1:4180'
const MODEL_PORT = Number(process.env.FLOW_INTRO_MODEL_PORT ?? 4390)
const OUT = path.resolve(root, process.env.FLOW_INTRO_OUT ?? 'public/media')
const FRAMES = process.env.FLOW_INTRO_FRAMES ?? path.join(OUT, '.frames')
const POSTER_AT = Number(process.env.FLOW_INTRO_POSTER_AT ?? 23)
const key = `orbit-${Date.now().toString(36)}`
const PROMPT = 'What is in progress this week?'
const ANSWER = [
  '**3 issues are in progress** in Orbit:\n\n',
  '1. **Fix flaky sync on slow connections** (high priority)\n',
  '2. **Redesign onboarding checklist**\n',
  '3. **Add dark mode to the mobile app**, moved here just now\n\n',
  'Nothing is blocked. The sync fix is the one to watch before Friday.',
]

// A tiny stand-in for the model provider so the agent scene is deterministic and needs no API key.
const sse = (response, type, data) => response.write(`event: ${type}\ndata: ${JSON.stringify({ type, ...data })}\n\n`)
const model = http.createServer(async (request, response) => {
  if (request.method === 'GET') return response.writeHead(200).end('ok')
  let raw = ''
  for await (const chunk of request) raw += chunk
  response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  sse(response, 'response.created', { response: { id: 'resp_demo' } })
  if (!raw.includes('function_call_output')) {
    sse(response, 'response.output_item.added', { item: { id: 'item_1', call_id: 'call_1', type: 'function_call', name: 'list_issues', arguments: '' } })
    sse(response, 'response.function_call_arguments.done', { item_id: 'item_1', arguments: '{"limit":8}' })
  } else {
    sse(response, 'response.reasoning_summary_text.delta', { delta: 'Checked the active issues in Orbit.' })
    await new Promise(resolve => setTimeout(resolve, 500))
    for (const delta of ANSWER) {
      sse(response, 'response.output_text.delta', { delta })
      await new Promise(resolve => setTimeout(resolve, 650))
    }
  }
  sse(response, 'response.completed', { response: { status: 'completed' } })
  response.end()
}).listen(MODEL_PORT, '127.0.0.1')

const headers = { 'content-type': 'application/json', 'X-Workspace-Key': key }
async function api(method, url, body) {
  const response = await fetch(API + url, { method, headers, body: body ? JSON.stringify(body) : undefined })
  if (!response.ok) throw new Error(`${method} ${url} -> ${response.status} ${await response.text()}`)
  return response.json()
}

async function seed() {
  const boot = await api('POST', '/api/workspaces', { name: 'Orbit', urlKey: key, region: 'us', tutorial: false })
  const team = boot.teams[0]
  const label = name => boot.labels.find(item => item.name === name)?.id
  const issues = [
    ['Fix flaky sync on slow connections', 'state_progress', 2, 'Bug'],
    ['Redesign onboarding checklist', 'state_progress', 3, 'Feature'],
    ['Speed up board drag and drop', 'state_todo', 3, 'Improvement'],
    ['Write release notes for v2.4', 'state_todo', 4, 'Improvement'],
    ['Add CSV export to reports', 'state_todo', 3, 'Feature'],
    ['Audit notification settings', 'state_todo', 4, 'Bug'],
    ['Publish the changelog page', 'state_done', 3, 'Feature'],
  ]
  for (const [title, stateId, priority, labelName] of issues) {
    await api('POST', '/api/issues', { title, teamId: team.id, stateId, priority, assigneeId: boot.viewer.id, labelIds: [label(labelName)].filter(Boolean) })
  }
  return team.key
}

const cursorScript = () => {
  const cursor = document.createElement('div')
  cursor.style.cssText = 'position:fixed;left:0;top:0;width:22px;height:22px;z-index:2147483647;pointer-events:none;transform:translate(-100px,-100px);filter:drop-shadow(0 1px 2px rgba(0,0,0,.35))'
  cursor.innerHTML = '<svg width="22" height="22" viewBox="0 0 22 22"><path d="M3 2l14 8-6 1.8L8.4 18z" fill="#fff" stroke="#111" stroke-width="1.4" stroke-linejoin="round"/></svg>'
  const ripple = document.createElement('div')
  ripple.style.cssText = 'position:fixed;left:0;top:0;width:34px;height:34px;margin:-17px 0 0 -17px;border-radius:50%;z-index:2147483646;pointer-events:none;background:rgba(94,106,210,.35);opacity:0;transform:scale(.3)'
  addEventListener('DOMContentLoaded', () => { document.body.append(ripple, cursor) })
  addEventListener('mousemove', event => { cursor.style.transform = `translate(${event.clientX}px,${event.clientY}px)` }, true)
  addEventListener('mousedown', event => {
    ripple.style.left = `${event.clientX}px`
    ripple.style.top = `${event.clientY}px`
    ripple.animate([{ opacity: 1, transform: 'scale(.3)' }, { opacity: 0, transform: 'scale(1.3)' }], { duration: 420, easing: 'ease-out' })
  }, true)
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const browser = await chromium.launch({ channel: process.env.CI ? undefined : 'chrome' })
rmSync(FRAMES, { recursive: true, force: true })
mkdirSync(FRAMES, { recursive: true })
mkdirSync(OUT, { recursive: true })
try {
  const teamKey = await seed()
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 }, deviceScaleFactor: 1.25, colorScheme: 'light', reducedMotion: 'no-preference' })
  await context.addInitScript(cursorScript)
  const page = await context.newPage()
  const base = `${WEB}/${key}`
  await page.goto(`${base}/team/${teamKey}/active`)
  await page.getByRole('button', { name: 'Display options' }).waitFor()
  // Tidy the sidebar's suggestion cards so the recording shows only the product.
  for (const name of ['Dismiss Invite people', 'Dismiss Cycles', 'Dismiss Connect GitHub']) {
    const button = page.getByRole('button', { name })
    if (await button.count()) await button.first().click()
  }
  await page.mouse.move(640, 420)
  await sleep(600)

  // Screencast capture.
  const cdp = await context.newCDPSession(page)
  const frames = []
  let t0 = null
  cdp.on('Page.screencastFrame', async ({ data, metadata, sessionId }) => {
    const index = frames.length
    const file = path.join(FRAMES, `f${String(index).padStart(5, '0')}.jpg`)
    writeFileSync(file, Buffer.from(data, 'base64'))
    t0 ??= metadata.timestamp
    frames.push({ file, t: metadata.timestamp - t0 })
    cdp.send('Page.screencastFrameAck', { sessionId }).catch(() => {})
  })
  await cdp.send('Page.startScreencast', { format: 'jpeg', quality: 90, maxWidth: 1600, maxHeight: 900, everyNthFrame: 1 })
  const started = Date.now()
  const now = () => (Date.now() - started) / 1000
  const cues = []
  const say = text => { cues.push({ start: now(), text }) }
  const move = async (locator, { dx = 0, dy = 0, steps = 28 } = {}) => {
    const box = await locator.boundingBox({ timeout: 5000 })
    await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy, { steps })
  }
  const click = async (locator, options) => {
    try {
      await move(locator, options)
      await sleep(250)
      await locator.click({ timeout: 5000 })
    } catch (error) {
      await page.screenshot({ path: path.join(OUT, 'error.png') })
      throw error
    }
    await sleep(450)
  }

  // 1. The issue list, then a new issue.
  say('Everything your team is working on, in one fast list.')
  await sleep(2600)
  say('Create an issue in a few keystrokes.')
  await click(page.getByRole('button', { name: 'New issue', exact: true }).first())
  await page.keyboard.type('Add dark mode to the mobile app', { delay: 55 })
  await sleep(500)
  await click(page.locator('[role=dialog] button[aria-label^="Change status"]'))
  await sleep(400)
  await click(page.getByRole('option', { name: /^Todo/ }).first())
  await sleep(300)
  await click(page.getByRole('button', { name: 'Create issue' }))
  say('Every issue opens into a full page of details.')
  await sleep(2600)

  // 2. The board.
  say('Switch to a board to see the flow of work.')
  await click(page.getByRole('link', { name: 'Issues', exact: true }).first())
  await sleep(900)
  await click(page.getByRole('button', { name: 'Display options' }))
  await click(page.getByText('Board', { exact: true }).last())
  await page.keyboard.press('Escape')
  await sleep(1500)

  // 3. Move the new card to In Progress.
  say('Drag a card to move it along.')
  const card = page.locator('a[data-board-card]', { hasText: 'Add dark mode to the mobile app' })
  const target = page.locator('section[data-board-column][aria-label="In Progress"]')
  await card.scrollIntoViewIfNeeded()
  await move(card)
  await sleep(300)
  await page.mouse.down()
  const to = await target.boundingBox()
  await page.mouse.move(to.x + to.width / 2, to.y + 260, { steps: 40 })
  await sleep(500)
  await page.mouse.move(to.x + to.width / 2 + 6, to.y + 200, { steps: 8 })
  await sleep(300)
  await page.mouse.up()
  await sleep(2200)

  // 4. The agent.
  say('Ask the Flow Agent about your work.')
  await click(page.getByRole('link', { name: 'Agent' }).first())
  const composer = page.getByRole('textbox').first()
  await click(composer, { dy: -10 })
  await page.keyboard.type(PROMPT, { delay: 55 })
  await sleep(500)
  await click(page.getByRole('button', { name: 'Send message' }))
  await sleep(5200)

  // 5. Closing card.
  say('Flow. Plan, build and ship together.')
  await page.setContent(`<!doctype html><meta charset=utf-8><body style="margin:0;height:100vh;display:grid;place-items:center;background:#101012;color:#fff;font-family:Inter,system-ui,sans-serif">
    <div style="display:grid;justify-items:center;gap:22px">
      <svg width="104" height="104" viewBox="4 4 40 40" fill="#fff"><mask id="m" maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48"><rect width="48" height="48" fill="#fff"/><path d="M0 33C11 33 14 13 24 13C31 13 33 26 37 26C40.5 26 42.5 20 48 17" fill="none" stroke="#000" stroke-width="5"/></mask><rect x="4" y="4" width="40" height="40" rx="10" mask="url(#m)"/></svg>
      <div style="font-size:44px;font-weight:560;letter-spacing:-.02em">Flow</div>
      <div style="font-size:18px;color:rgba(255,255,255,.6)">Plan, build and ship together.</div>
    </div></body>`)
  await sleep(2600)
  const total = now()
  await cdp.send('Page.stopScreencast')
  void 0
  await sleep(300)

  // Stitch: each frame holds until the next one arrives.
  const lines = ['ffconcat version 1.0']
  frames.forEach((frame, index) => {
    const next = index + 1 < frames.length ? frames[index + 1].t : Math.max(total, frame.t + 0.1)
    lines.push(`file '${frame.file}'`, `duration ${Math.max(next - frame.t, 0.001).toFixed(4)}`)
  })
  lines.push(`file '${frames.at(-1).file}'`)
  writeFileSync(path.join(FRAMES, 'list.txt'), lines.join('\n'))
  const duration = Math.max(total, frames.at(-1).t + 0.1)
  const run = (args) => {
    const result = spawnSync('ffmpeg', ['-y', '-loglevel', 'error', ...args], { stdio: 'inherit' })
    if (result.status !== 0) throw new Error(`ffmpeg ${args.join(' ')} failed`)
  }
  const input = ['-f', 'concat', '-safe', '0', '-i', path.join(FRAMES, 'list.txt')]
  const filter = ['-vf', 'fps=30,scale=1600:900:flags=lanczos,format=yuv420p', '-t', duration.toFixed(2)]
  run([...input, ...filter, '-an', '-c:v', 'libx264', '-preset', 'slow', '-crf', '27', '-movflags', '+faststart', path.join(OUT, 'flow-intro.mp4')])
  run([...input, ...filter, '-an', '-c:v', 'libvpx-vp9', '-crf', '38', '-b:v', '0', '-row-mt', '1', path.join(OUT, 'flow-intro.webm')])
  run(['-i', path.join(OUT, 'flow-intro.mp4'), '-ss', String(POSTER_AT), '-frames:v', '1', '-q:v', '4', path.join(OUT, 'flow-intro-poster.jpg')])

  const stamp = seconds => {
    const ms = Math.round(seconds * 1000)
    const part = (value, size) => String(value).padStart(size, '0')
    return `${part(Math.floor(ms / 3600000), 2)}:${part(Math.floor(ms / 60000) % 60, 2)}:${part(Math.floor(ms / 1000) % 60, 2)}.${part(ms % 1000, 3)}`
  }
  const vtt = ['WEBVTT', '']
  cues.forEach((cue, index) => {
    const end = index + 1 < cues.length ? cues[index + 1].start : duration
    vtt.push(`${stamp(cue.start)} --> ${stamp(end - 0.1)}`, cue.text, '')
  })
  writeFileSync(path.join(OUT, 'flow-intro.en.vtt'), vtt.join('\n'))
  console.log(`frames ${frames.length}, duration ${duration.toFixed(1)}s`)
  for (const file of readdirSync(OUT).filter(name => name.startsWith('flow-intro'))) console.log(file, (statSync(path.join(OUT, file)).size / 1024).toFixed(0), 'KB')
} finally {
  await browser.close()
  model.close()
  rmSync(FRAMES, { recursive: true, force: true })
}
