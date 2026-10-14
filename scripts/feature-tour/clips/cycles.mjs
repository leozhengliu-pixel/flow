// Planning: the current cycle with its burn-up and workload.
export default {
  title: 'Cycles keep the team on a steady rhythm',
  subtitle: 'Scope, progress and workload for the current sprint.',
  eyebrow: 'Planning',
  startPath: '/team/WEB/all',
  async run(s) {
    const { page } = s
    await s.caption('Open the current cycle from the sidebar')
    await s.click(page.locator('a:has-text("Current")'), { duration: 900, settle: 1400 })

    await s.caption('Scope, started and completed work at a glance')
    await s.focusRect({ x: 960, y: 90, w: 480, h: 360 }, { maxScale: 1.8, duration: 1000 })
    await s.pause(1700)

    await s.caption('See who carries how much of the cycle')
    await s.focusRect({ x: 960, y: 400, w: 480, h: 380 }, { maxScale: 1.8, duration: 1000 })
    await s.moveTo('text=Lucas Meyer', { duration: 700 })
    await s.pause(1500)
    await s.unfocus(900)
    await s.caption(null)
  },
}
