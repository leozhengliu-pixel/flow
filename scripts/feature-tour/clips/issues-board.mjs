// Core workflow: the issue list, then the same issues as a board.
export default {
  title: 'Triage work in list and board views',
  subtitle: 'Every issue in one fast list, one click from a board.',
  eyebrow: 'Core workflow',
  startPath: '/team/WEB/all',
  // The clip moves this card to In Progress; put it back for the next take.
  async prepare(demo) {
    await demo.patchIssue('Guest checkout: skip account creation', { stateId: 'state_todo' })
  },
  async run(s) {
    const { page } = s
    await s.caption('One list for everything your team is working on')
    await s.moveTo('text=Redesign cart summary with itemized taxes', { duration: 900 })
    await s.pause(600)
    await s.moveTo('text=Address form loses focus after a validation error', { duration: 700 })
    await s.pause(800)

    await s.caption('Switch layouts from Display options')
    await s.click('[aria-label="Display options"]')
    await s.focusRect({ x: 900, y: 60, w: 540, h: 600 }, { maxScale: 1.5 })
    await s.pause(500)
    await s.click(page.getByText('Board', { exact: true }), { duration: 800 })
    await s.pause(900)
    await s.unfocus(700)
    await s.press('Escape', { settle: 700 })

    await s.caption('Statuses become columns')
    await s.focusRect({ x: 240, y: 90, w: 1130, h: 640 }, { maxScale: 1.3, duration: 1100 })
    await s.pause(1400)

    await s.caption('Drag a card to change its status')
    await s.drag('text=Guest checkout: skip account creation', page.locator('text=Implement tax breakdown component').first(), { duration: 1100 })
    await s.pause(1700)
    await s.unfocus(900)
    await s.caption(null)
  },
}
