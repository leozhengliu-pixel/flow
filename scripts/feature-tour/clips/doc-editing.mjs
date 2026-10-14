// Documents & collaboration: writing in the editor - headings, lists, formatting.
const TITLE = 'Onboarding research summary'
const WIDE = { width: 1920, height: 1200 }

export default {
  viewport: WIDE,
  title: 'A calm editor for specs and notes',
  subtitle: 'Slash commands, lists and rich formatting.',
  eyebrow: 'Documents',
  async startPath(demo) {
    const doc = await demo.restoreDocument(TITLE)
    return `/document/${doc.slugId}`
  },
  async cleanup(demo) {
    await demo.restoreDocument(TITLE)
  },
  async run(s) {
    const { page } = s
    const visible = (text) => page.getByText(text, { exact: true }).filter({ visible: true })
    await s.pause(3200) // the collaborative editor needs a moment before it takes input
    await s.caption('Pick up where the notes end')
    await s.click(visible('Track activation as "first issue created within one day"'), { duration: 900 })
    await s.press('End', { settle: 200 })
    await s.press('Enter', { settle: 150 })
    await s.press('Enter', { settle: 400 })

    await s.caption('Type / for headings, lists, code and more')
    await s.type('/', { delay: 80 })
    await s.pause(900)
    await s.click(visible('Heading 2'), { duration: 650, settle: 400 })
    await s.type('Open questions', { delay: 40, jitter: 25 })
    await s.press('Enter', { settle: 300 })
    await s.type('/', { delay: 80 })
    await s.pause(700)
    await s.click(visible('Bulleted list'), { duration: 600, settle: 400 })
    await s.type('Localize the checklist for new markets?', { delay: 30, jitter: 20 })
    await s.press('Enter', { settle: 200 })
    await s.type('Which events define an activated team?', { delay: 30, jitter: 20 })
    await s.pause(700)

    await s.caption('Select text to format it')
    const heading = await s.rectOf('t:Open questions')
    const last = await s.rectOf(visible('Which events define an activated team?'))
    await s.focusRect({ x: heading.x - 120, y: heading.y - 110, w: 760, h: last.y + last.height - heading.y + 200 }, { maxScale: 1.7, duration: 900 })
    await s.pause(300)
    const bullet = visible('Localize the checklist for new markets?')
    await s.click(bullet, { duration: 700, settle: 200, ring: false, button: 'left' })
    await page.getByText('Localize the checklist for new markets?').first().click({ clickCount: 3 })
    await s.pause(700)
    await s.click('[aria-label="Bold"]', { duration: 600, settle: 500 })
    await s.click('[aria-label="Italic"]', { duration: 450, settle: 700 })
    await s.pause(900)
    await s.unfocus(800)
    await s.caption(null)
  },
}
