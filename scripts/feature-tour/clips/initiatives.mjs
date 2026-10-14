// Planning: initiatives group projects toward a goal.
export default {
  title: 'Initiatives connect projects to goals',
  subtitle: 'Track several projects against one outcome.',
  eyebrow: 'Planning',
  startPath: '/initiatives',
  async run(s) {
    await s.caption('Initiatives group projects toward a company goal')
    await s.moveTo('t:Grow self-serve revenue', { duration: 900 })
    await s.pause(700)
    await s.moveTo('t:Platform reliability', { duration: 500 })
    await s.pause(500)
    await s.click('t:Grow self-serve revenue', { duration: 600, settle: 1400 })

    await s.caption('Owner, target date and health at a glance')
    await s.pause(1400)
    await s.caption(null)
  },
}
