// Core workflow: create an issue from the dialog.
const TITLE = 'Add saved cards to checkout'

export default {
  speed: 1.3,
  title: 'Capture an issue in seconds',
  subtitle: 'Title, properties, and done - without leaving the list.',
  eyebrow: 'Core workflow',
  startPath: '/team/WEB/all',
  async prepare(demo) {
    await demo.purgeIssues([TITLE])
  },
  async run(s) {
    const { page } = s
    const visible = (text) => page.getByText(text, { exact: true }).filter({ visible: true })
    const quick = { duration: 380 }
    await s.caption('Create an issue from anywhere')
    await s.click('[aria-label="Create new issue"]', { duration: 700, settle: 500 })
    await s.focusRect({ x: 330, y: 100, w: 780, h: 520 }, { maxScale: 1.55, duration: 800 })

    await s.caption('Give it a title and a description')
    await s.type(TITLE, { delay: 34, jitter: 20 })
    await s.click(page.locator('[contenteditable="true"]').last(), { ...quick, at: 'left', dx: 60 })
    await s.type('One-click payments for returning customers.', { delay: 22, jitter: 14 })
    await s.pause(200)

    await s.caption('Set priority, assignee, project and labels')
    await s.click(visible('Priority'), { ...quick, settle: 300, expect: visible('No priority') })
    await s.click(visible('High').last(), { ...quick, settle: 250 })
    await s.click(visible('Maya Chen').first(), { ...quick, settle: 300, expect: visible('No assignee') })
    await s.click(visible('Priya Raman').last(), { ...quick, settle: 250 })
    await s.click(visible('Project'), { ...quick, settle: 300, expect: visible('No project') })
    await s.click(visible('Checkout redesign').last(), { ...quick, settle: 250 })
    await s.click(visible('Labels'), { ...quick, settle: 300, expect: visible('Bug') })
    await s.click(visible('Feature').last(), { ...quick, settle: 250 })
    await s.press('Escape', { settle: 300 })

    await s.caption('Create it and you are straight into the new issue')
    await s.click('t:Create issue', { duration: 500, settle: 500 })
    await s.unfocus(700)
    await s.pause(1300)
    await s.caption(null)
  },
}
