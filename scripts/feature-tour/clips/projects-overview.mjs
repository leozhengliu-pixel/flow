// Planning: from the projects list into a project overview.
import { projectsListStorage } from '../lib/demo.mjs'

export default {
  storage: projectsListStorage,
  title: 'Projects that tell you where things stand',
  subtitle: 'Health, milestones and progress at a glance.',
  eyebrow: 'Planning',
  startPath: '/projects/all',
  async run(s) {
    const { page } = s
    await s.caption('Every project with its health, lead and progress')
    await s.moveTo('t:API rate limiting', { duration: 900 })
    await s.pause(500)
    await s.moveTo('t:Mobile app 3.0', { duration: 600 })
    await s.pause(700)
    await s.click('t:Checkout redesign', { duration: 600, settle: 1300 })

    await s.caption('Properties, dates and teams in one panel')
    await s.focusRect({ x: 1010, y: 90, w: 430, h: 400 }, { maxScale: 1.9, duration: 1000 })
    await s.moveTo('t:Priya Raman', { duration: 700, dx: 120 })
    await s.pause(1500)

    await s.caption('Milestones show progress toward each target')
    await s.focusRect({ x: 1010, y: 460, w: 430, h: 220 }, { maxScale: 2, duration: 1000 })
    await s.moveTo('t:Design sign-off', { duration: 700 })
    await s.pause(500)
    await s.moveTo('t:Private beta', { duration: 500 })
    await s.pause(1200)

    await s.caption('Project updates keep everyone in the loop')
    await s.focusRect({ x: 270, y: 470, w: 740, h: 200 }, { maxScale: 1.7, duration: 1000 })
    await s.moveTo('text=Design sign-off is on track', { duration: 800 })
    await s.pause(1800)
    await s.unfocus(900)
    await s.caption(null)
  },
}
