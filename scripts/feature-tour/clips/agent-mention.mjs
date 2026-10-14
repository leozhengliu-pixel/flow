// Agent: mention @Flow in an issue comment; the agent works and replies in the thread.
const ISSUE = 'Checkout page LCP over 4s on slow 3G'

let comments
let issues

export default {
  title: 'Mention @Flow in any issue',
  subtitle: 'The agent picks it up, works, and answers in the thread.',
  eyebrow: 'Agent',
  startPath: '/issue/WEB-9',
  speed: 1.5,
  async prepare(demo) {
    comments = await demo.commentIds(ISSUE)
    issues = await demo.snapshotIssues()
  },
  async cleanup(demo) {
    if (comments) await demo.purgeCommentsAfter(comments)
    if (issues) await demo.purgeIssuesAfter(issues)
    await demo.clearAgentSessions()
    await demo.resetInbox()
  },
  async run(s) {
    const { page } = s
    await s.settled(1200)
    await s.caption('Comment and mention the agent')
    await s.click('[contenteditable="true"][aria-label^="Leave a comment"]', { duration: 900, settle: 300 })
    await s.type('@Flo', { delay: 70 })
    await s.pause(700)
    await s.click(page.getByText('Flow', { exact: true }).filter({ visible: true }).last(), { duration: 600, settle: 300, approach: 'horizontal' })
    await s.type('can you suggest a plan to get this under 2.5s? Reply here, do not change the issue.', { delay: 28, jitter: 18 })
    await s.pause(600)
    await s.click('button:has(svg.lucide-arrow-up)', { duration: 700, settle: 600 })

    await s.caption('Flow shows it is working on it')
    await s.pause(2500)
    // The agent's reply is stamped "just now"; the working row is not.
    await page.getByText('just now', { exact: true }).filter({ visible: true }).first().waitFor({ state: 'visible', timeout: 150_000 })
    await s.pause(1200)

    await s.caption('...and replies in the thread')
    await s.pause(800)
    await s.focusRect({ x: 270, y: 330, w: 700, h: 420 }, { maxScale: 1.4, duration: 1000 })
    await s.pause(3500)
    await s.unfocus(900)
    await s.caption(null)
  },
}
