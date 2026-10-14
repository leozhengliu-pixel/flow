// Planning: post a project update with a health status.
import { projectsListStorage } from '../lib/demo.mjs'

export default {
  storage: projectsListStorage,
  title: 'Post a project update in seconds',
  subtitle: 'Pick the health, say what changed, keep everyone in the loop.',
  eyebrow: 'Planning',
  startPath: '/projects/all',
  async prepare(demo) {
    const project = await demo.project('Onboarding revamp')
    const updates = await demo.maya.get(`/api/projects/${project.id}/updates`)
    for (const update of updates.nodes ?? updates) await demo.maya.delete(`/api/projects/${project.id}/updates/${update.id}`).catch(() => {})
  },
  async run(s) {
    const { page } = s
    await s.caption('Open a project and write its first update')
    await s.click('t:Onboarding revamp', { duration: 800, settle: 1100 })
    await s.click('t:Write first project update', { duration: 600, settle: 800 })
    await s.click(page.getByText('Update', { exact: true }), { duration: 450 })
    await s.pause(300)

    await s.caption('Set the health, then write what changed')
    await s.focusRect({ x: 250, y: 120, w: 800, h: 270 }, { maxScale: 1.6, duration: 800 })
    await s.click('t:On track', { duration: 500 })
    await s.pause(450)
    await s.moveTo('t:At risk', { duration: 350 })
    await s.pause(350)
    await s.click(page.getByText('On track', { exact: true }).filter({ visible: true }).last(), { duration: 350 })
    await s.click(page.locator('[contenteditable="true"]'), { duration: 500, at: 'left', dx: 80 })
    await s.type('Signup form is next. Checklist is on track for Nov 3.', { delay: 34, jitter: 20 })
    await s.pause(500)

    await s.caption('Post it to the whole team')
    await s.click('t:Post update', { duration: 600, settle: 1500 })
    await s.pause(900)
    await s.unfocus(800)
    await s.caption(null)
  },
}
