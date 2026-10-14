// Agent: the agent asks a clarifying question with option pills, then acts on the answer.
const PROMPT = 'I want to file a new bug about slow search results. Ask me which team it belongs to before you create anything.'

let before

export default {
  title: 'The agent asks when it needs to',
  subtitle: 'Answer with one click and it carries on.',
  eyebrow: 'Agent',
  startPath: '/agent',
  speed: 1.4,
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
    await s.caption('Describe what you want done')
    await s.click('[contenteditable="true"][aria-label="Send a message to Flow AI"]', { duration: 900, settle: 300 })
    await s.type(PROMPT, { delay: 26, jitter: 18 })
    await s.pause(500)
    await page.keyboard.press('Enter')

    await s.caption('When something is ambiguous, it asks')
    const after = (name) => page.locator('text=/Which team should/').locator(`xpath=following::button[normalize-space()="${name}"][1]`)
    const pill = after('Platform')
    await pill.waitFor({ state: 'visible', timeout: 90_000 })
    await s.pause(900)
    const box = await s.rectOf(pill)
    await s.focusRect({ x: box.x - 60, y: box.y - 120, w: 560, h: 220 }, { maxScale: 1.9, duration: 900 })
    await s.pause(700)
    await s.moveTo(after('Web'), { duration: 700 })
    await s.pause(350)

    await s.caption('Pick an option and it continues')
    await s.click(pill, { duration: 500, settle: 500 })
    await s.unfocus(900)
    await s.untilQuiet({ quiet: 4000, max: 120_000 })
    await s.pause(1500)
    await s.caption(null)
  },
}
