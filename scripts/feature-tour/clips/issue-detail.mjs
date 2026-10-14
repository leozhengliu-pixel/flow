// Core workflow: open an issue and tour properties, sub-issues and activity.
export default {
  title: 'Everything about an issue, in one place',
  subtitle: 'Properties, sub-issues, and a full activity trail.',
  eyebrow: 'Core workflow',
  startPath: '/team/WEB/all',
  async run(s) {
    const { page } = s
    await s.caption('Open an issue from the list')
    await s.click(page.locator('a[href*="/issue/WEB-3/"]'), { duration: 900, at: 'left', dx: 160 })
    await s.settled(900)

    await s.caption('Properties live in the sidebar')
    const visible = (text) => page.getByText(text, { exact: true }).filter({ visible: true })
    // Popovers are positioned in page coordinates, so open them before the camera zooms in.
    await s.moveTo(visible('In Progress').first(), { duration: 900 })
    await s.pause(350)
    await s.click(visible('Urgent').first(), { duration: 600, expect: visible('No priority') })
    await s.focusRect({ x: 1000, y: 120, w: 440, h: 460 }, { maxScale: 1.8 })
    await s.pause(500)
    await s.moveTo(visible('High').last(), { duration: 500 })
    await s.pause(450)
    await s.moveTo(visible('Medium').last(), { duration: 450 })
    await s.pause(450)
    await s.click(visible('Urgent').last(), { duration: 500 })
    await s.pause(900)

    await s.caption('Break work into sub-issues')
    await s.focusRect({ x: 290, y: 360, w: 700, h: 200 }, { maxScale: 1.7, duration: 1000 })
    await s.moveTo('text=Implement tax breakdown component', { duration: 800 })
    await s.pause(500)
    await s.moveTo('text=Add unit tests for tax rounding', { duration: 600 })
    await s.pause(900)

    await s.caption('Every change and comment lands in the activity feed')
    await s.focusRect({ x: 290, y: 520, w: 700, h: 220 }, { maxScale: 1.7, duration: 1000 })
    await s.pause(2000)
    await s.unfocus(900)
    await s.caption(null)
  },
}
