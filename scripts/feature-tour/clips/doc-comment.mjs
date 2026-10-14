// Documents & collaboration: an inline comment thread on a selection.
const TITLE = 'API rate limiting: design notes'
const SENTENCE = 'Each workspace gets a token bucket per API key. Requests consume one token; tokens refill at a steady rate with a burst allowance.'

export default {
  viewport: { width: 1920, height: 1200 },
  title: 'Discuss right where the text is',
  subtitle: 'Inline comment threads anchored to your selection.',
  eyebrow: 'Documents',
  async startPath(demo) {
    const doc = await demo.restoreDocument(TITLE)
    return `/document/${doc.slugId}`
  },
  async cleanup(demo) {
    await demo.restoreDocument(TITLE)
  },
  async run(s) {
    const { page } = s
    await s.pause(3200)
    await s.caption('Select the passage you want to discuss')
    const line = page.getByText(SENTENCE, { exact: false }).filter({ visible: true }).first()
    await s.moveTo(line, { duration: 1000 })
    await s.pause(300)
    await line.click({ clickCount: 3 })
    await s.pause(800)
    const box = await s.rectOf(line)
    await s.focusRect({ x: box.x - 40, y: box.y - 110, w: 1250, h: 560 }, { maxScale: 1.5, duration: 900 })
    await s.pause(500)

    await s.caption('Start a thread from the toolbar')
    await s.click('[aria-label="Comment"]', { duration: 650, settle: 900 })
    await s.type('Do we want per-endpoint limits for the search API as well?', { delay: 30, jitter: 20 })
    await s.pause(500)
    await page.keyboard.press('Enter')
    await s.pause(1100)

    await s.caption('Teammates reply in the thread')
    await s.click(page.locator('.document-thread-card [contenteditable="true"]').last(), { duration: 650, settle: 500 })
    await s.type('Yes, but only after shadow mode shows real traffic.', { delay: 30, jitter: 20 })
    await s.pause(400)
    await page.keyboard.press('Enter')
    await s.pause(1000)

    await s.caption('React with an emoji, or resolve the thread')
    await s.click(page.locator('.document-thread-card [aria-label="Add reaction"]').first(), { duration: 700, settle: 700 })
    await s.pause(500)
    await s.click(page.getByText('👍', { exact: true }).filter({ visible: true }), { duration: 600, settle: 900 })
    await s.pause(1500)
    await s.unfocus(900)
    await s.caption(null)
  },
}
