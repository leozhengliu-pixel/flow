// Core workflow: the inbox - read a notification, snooze another.
export default {
  title: 'Stay on top of what needs you',
  subtitle: 'Mentions, comments and status changes land in one inbox.',
  eyebrow: 'Core workflow',
  startPath: '/inbox',
  async prepare(demo) {
    await demo.resetInbox()
  },
  async run(s) {
    const { page } = s
    await s.caption('Mentions, comments and changes land in one inbox')
    await s.moveTo('[aria-label^="Lucas Meyer WEB-3"]', { duration: 900 })
    await s.pause(500)
    await s.moveTo('[aria-label^="Sofia Alvarez WEB-11"]', { duration: 500 })
    await s.pause(400)
    await s.moveTo('[aria-label^="Daniel Okafor PLT-32"]', { duration: 500 })
    await s.pause(500)

    await s.caption('Open a notification to read it in context')
    await s.click('[aria-label^="Daniel Okafor PLT-32"]', { settle: 1200 })
    await s.focusRect({ x: 640, y: 90, w: 800, h: 560 }, { maxScale: 1.45, duration: 1000 })
    await s.pause(1600)
    await s.unfocus(800)

    await s.caption('Snooze what can wait')
    await s.moveTo('[aria-label^="Lucas Meyer WEB-8"]', { duration: 700 })
    await s.pause(400)
    await s.click(page.locator('[aria-label="Snooze notification"]').first(), { duration: 450 })
    await s.focusRect({ x: 340, y: 40, w: 560, h: 380 }, { maxScale: 1.7, duration: 900 })
    await s.pause(500)
    await s.click('t:Tomorrow', { duration: 600, settle: 1300 })
    await s.unfocus(800)
    await s.pause(500)
    await s.caption(null)
  },
}
