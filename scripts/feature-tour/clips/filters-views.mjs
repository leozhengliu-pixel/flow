// Core workflow: filter the issue list and save the filters as a view.
export default {
  title: 'Filter anything, save it as a view',
  subtitle: 'Combine filters and keep them one click away.',
  eyebrow: 'Core workflow',
  startPath: '/team/WEB/all',
  speed: 1.15,
  async prepare(demo) {
    await demo.deleteViews() // the seeded workspace has no saved views
  },
  async run(s) {
    const { page } = s
    const quick = { duration: 450, settle: 350 }
    await s.caption('Filter by assignee, priority, label and more')
    await s.click('[aria-label="Add filter"]', { duration: 800, settle: 500, expect: 't:Advanced filter' })
    await s.moveTo('t:Assignee', { duration: 450 }) // submenus open on hover
    await s.pause(550)
    await s.click('t:Priya Raman', { ...quick, approach: 'horizontal' })
    await s.press('Escape', { settle: 500 })
    await s.click('[aria-label="Add another filter"]', { ...quick, expect: 't:Advanced filter' })
    await s.moveTo('t:Priority', { duration: 450 })
    await s.pause(550)
    await s.click('t:Urgent', { ...quick, approach: 'horizontal' })
    await s.press('Escape', { settle: 600 })
    await s.focusRect({ x: 340, y: 80, w: 820, h: 360 }, { maxScale: 1.55, duration: 900 })
    await s.pause(1200)
    await s.unfocus(800)

    await s.caption('Turn the filters into a saved view')
    await s.click('[aria-label="Add new view"]', { duration: 700, settle: 900 })
    await s.focusRect({ x: 340, y: 60, w: 1100, h: 340 }, { maxScale: 1.3, duration: 900 })
    await s.pause(800)
    await s.click('t:Create view', { duration: 700, settle: 1400 })
    await s.pause(900)
    await s.unfocus(800)
    await s.caption(null)
  },
}
