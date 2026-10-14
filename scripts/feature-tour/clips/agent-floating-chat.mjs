// Agent: the floating chat opened from a document selection ("Ask agent").
const TITLE = 'Checkout redesign: product spec'
const SELECTION = 'Support wallet payments on every device'

let before

export default {
  title: 'Ask the agent about any selection',
  subtitle: 'Select text in a document and the floating chat opens with it as context.',
  eyebrow: 'Agent',
  speed: 1.8,
  async startPath(demo) {
    await demo.clearAgentSessions()
    before = await demo.snapshotIssues()
    const doc = await demo.restoreDocument(TITLE)
    return `/document/${doc.slugId}`
  },
  async cleanup(demo) {
    if (before) await demo.purgeIssuesAfter(before)
    await demo.clearAgentSessions()
    await demo.restoreDocument(TITLE)
  },
  async run(s) {
    const { page } = s
    await s.pause(3200)
    await s.caption('Select text in a document')
    const line = page.getByText(SELECTION, { exact: true }).filter({ visible: true }).first()
    await s.moveTo(line, { duration: 1000 })
    await s.pause(250)
    await line.click({ clickCount: 3 })
    await s.pause(800)
    const box = await s.rectOf(line)
    await s.focusRect({ x: box.x - 140, y: box.y - 100, w: 780, h: 220 }, { maxScale: 1.8, duration: 800 })
    await s.pause(500)

    await s.caption('Ask agent')
    await s.click('[aria-label="Ask agent"]', { duration: 700, settle: 1200 })
    await s.unfocus(800)
    await s.pause(600)

    await s.caption('The selection travels with your question')
    await s.type('Which open issues already cover this? Just list them, do not change anything.', { delay: 32, jitter: 22 })
    await s.pause(500)
    await page.keyboard.press('Enter')
    await s.pause(5000)
    await s.untilQuiet({ quiet: 3000 })
    const chat = page.locator('[class*="float" i]').filter({ visible: true }).first()
    await s.focusRect({ x: 960, y: 480, w: 480, h: 420 }, { maxScale: 1.7, duration: 900 })
    await s.pause(2600)
    await s.unfocus(900)
    await s.caption(null)
  },
}
