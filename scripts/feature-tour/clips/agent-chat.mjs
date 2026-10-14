// Agent: ask the Agent page a question; it reads the workspace and answers with a table.
const PROMPT = 'Which Web team issues are in progress? Show them as a table with assignee and priority.'

let before

export default {
  title: 'Ask Flow about your work',
  subtitle: 'The agent reads your workspace and answers with live data.',
  eyebrow: 'Agent',
  startPath: '/agent',
  speed: 1.6,
  async prepare(demo) {
    await demo.clearAgentSessions()
    before = await demo.snapshotIssues()
  },
  async cleanup(demo) {
    if (before) await demo.purgeIssuesAfter(before)
    await demo.clearAgentSessions()
  },
  async run(s) {
    const { page } = s
    await s.pause(1500)
    await s.caption('Ask in plain language')
    await s.click('[contenteditable="true"][aria-label="Send a message to Flow AI"]', { duration: 900, settle: 300 })
    await s.type(PROMPT, { delay: 30, jitter: 22 })
    await s.pause(500)
    await s.click('[aria-label="Send"], button:has(svg.lucide-arrow-up)', { duration: 600, settle: 300 }).catch(async () => page.keyboard.press('Enter'))
    await s.caption('It looks things up with tools as it goes')
    await s.pause(6000)
    await s.untilQuiet({ quiet: 3000 })

    await s.caption('Answers come back as tables and live issue rows')
    const table = page.locator('table').filter({ visible: true }).first()
    await table.scrollIntoViewIfNeeded()
    await s.pause(500)
    const box = await s.rectOf(table)
    await s.focusRect({ x: box.x - 40, y: box.y - 40, w: box.width + 80, h: box.height + 80 }, { maxScale: 1.4, duration: 1000 })
    await s.moveTo(table, { duration: 900 })
    await s.pause(2200)
    await s.unfocus(900)
    await s.caption(null)
  },
}
