// Documents & collaboration: a comment thread with replies and reactions on an issue.
const REPLY = 'Agreed. I will review the PR as soon as it is up.'

export default {
  title: 'Threads, replies and reactions',
  subtitle: 'Keep the conversation next to the work.',
  eyebrow: 'Collaboration',
  startPath: '/issue/WEB-8',
  async prepare(demo) {
    const issue = await demo.issue('Address form loses focus after a validation error')
    const boot = await demo.boot()
    for (const comment of boot.comments?.[issue.id] ?? []) {
      if (comment.body === REPLY) await demo.maya.delete(`/api/issues/${issue.id}/comments/${comment.id}`).catch(() => {})
      if ((comment.reactions?.['👀'] ?? []).includes(boot.viewer.id)) await demo.maya.post(`/api/issues/${issue.id}/comments/${comment.id}/reactions`, { emoji: '👀' })
    }
    await demo.emptyTrash()
  },
  async run(s) {
    const { page } = s
    await s.settled(900)
    await s.caption('Scroll to the discussion')
    await s.glide({ x: 900, y: 600 }, 500)
    for (let i = 0; i < 14; i++) {
      await page.mouse.wheel(0, 70)
      await s.pause(40)
    }
    await s.pause(700)

    await s.caption('Replies are grouped into threads')
    const first = page.getByText('Reproduced on Safari 18 and Chrome.', { exact: false }).filter({ visible: true }).first()
    const box = await s.rectOf(first)
    await s.focusRect({ x: box.x - 60, y: box.y - 60, w: 1000, h: 700 }, { maxScale: 1.45, duration: 900 })
    await s.moveTo(first, { duration: 700 })
    await s.pause(700)

    await s.caption('React with an emoji')
    await s.moveTo(first, { duration: 300, dx: 300 })
    await s.click(page.locator('[aria-label="Add reaction"]').filter({ visible: true }).nth(1), { duration: 600, settle: 800 })
    await s.click(page.getByText('👀', { exact: true }).filter({ visible: true }), { duration: 600, settle: 1000 })
    await s.pause(500)

    await s.caption('Reply right in the thread')
    await s.click('button.comment-thread__reply', { duration: 700, settle: 500 })
    await s.click('[contenteditable="true"][aria-label^="Leave a reply"]', { duration: 400, settle: 300, ring: false })
    await s.type(REPLY, { delay: 30, jitter: 20 })
    await s.pause(500)
    await page.keyboard.press('Enter')
    await s.pause(1800)
    await s.unfocus(900)
    await s.caption(null)
  },
}
