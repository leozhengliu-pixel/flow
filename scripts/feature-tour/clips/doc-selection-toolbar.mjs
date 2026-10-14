// Documents & collaboration: the selection toolbar, ending in "Create issue from selection".
const TITLE = 'Checkout redesign: product spec'
const SELECTION = 'Show taxes and shipping before the payment step'

export default {
  viewport: { width: 1920, height: 1200 },
  title: 'Turn any sentence into work',
  subtitle: 'Format, comment, ask the agent, or create an issue from a selection.',
  eyebrow: 'Documents',
  async startPath(demo) {
    await demo.purgeIssues([SELECTION])
    const doc = await demo.restoreDocument(TITLE)
    return `/document/${doc.slugId}`
  },
  async cleanup(demo) {
    await demo.purgeIssues([SELECTION])
    await demo.restoreDocument(TITLE)
  },
  async run(s) {
    const { page } = s
    await s.pause(3200)
    await s.caption('Select text and a toolbar appears')
    const line = page.getByText(SELECTION, { exact: true }).filter({ visible: true }).first()
    await s.moveTo(line, { duration: 1000 })
    await s.pause(300)
    await line.click({ clickCount: 3 })
    await s.pause(900)
    const box = await s.rectOf(line)
    await s.focusRect({ x: box.x - 140, y: box.y - 100, w: 780, h: 220 }, { maxScale: 1.9, duration: 900 })
    await s.pause(900)

    await s.caption('Format, link, quote or comment')
    for (const label of ['Bold', 'Link', 'Quote', 'Comment']) {
      await s.moveTo(`[aria-label="${label}"]`, { duration: 380 })
      await s.pause(380)
    }

    await s.caption('Create an issue from the selection')
    await s.moveTo('[aria-label="Create issue from selection"]', { duration: 500 })
    await s.pause(500)
    await s.click('[aria-label="Create issue from selection"]', { duration: 250, settle: 500 })
    await s.pause(600)
    await s.unfocus(800)
    const toast = page.getByText('Issue created', { exact: true }).filter({ visible: true }).first()
    await toast.waitFor({ state: 'visible', timeout: 6000 })
    const tbox = await toast.boundingBox()
    await s.focusRect({ x: tbox.x - 120, y: tbox.y - 40, w: 560, h: 190 }, { maxScale: 2, duration: 900 })
    await s.pause(1700)
    await s.unfocus(800)
    await s.caption(null)
  },
}
