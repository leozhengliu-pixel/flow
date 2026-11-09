import { expect } from '@playwright/test'
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import ts from 'typescript'
import { test } from './workspace-fixture'

function consumers() {
  const result: Array<{ label: string; classes: Array<string | { file: string; key: string }>; parent: string }> = []
  const visitDirectory = (directory: string) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const file = path.join(directory, entry.name)
      if (entry.isDirectory()) { visitDirectory(file); continue }
      if (!file.endsWith('.tsx') || /\.test\./.test(file)) continue
      const source = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
      const imports = new Map<string, string>()
      for (const node of source.statements) {
        if (ts.isImportDeclaration(node) && node.importClause?.name && ts.isStringLiteral(node.moduleSpecifier) && node.moduleSpecifier.text.endsWith('.module.css')) {
          imports.set(node.importClause.name.text, `../${path.relative(process.cwd(), path.resolve(path.dirname(file), node.moduleSpecifier.text))}`)
        }
      }
      const walk = (node: ts.Node) => {
        if ((ts.isJsxSelfClosingElement(node) || ts.isJsxOpeningElement(node)) && node.tagName.getText(source) === 'UserAvatar') {
          const classes: Array<string | { file: string; key: string }> = []
          const attribute = node.attributes.properties.find(value => ts.isJsxAttribute(value) && value.name.getText(source) === 'className')
          if (attribute && ts.isJsxAttribute(attribute) && attribute.initializer) {
            if (ts.isStringLiteral(attribute.initializer)) classes.push(attribute.initializer.text)
            else {
              for (const match of attribute.initializer.getText(source).matchAll(/\b(\w+)\.(\w+)/g)) {
                const cssFile = imports.get(match[1])
                if (cssFile) classes.push({ file: cssFile, key: match[2] })
              }
            }
          }
          result.push({ label: `${path.relative(process.cwd(), file)}:${source.getLineAndCharacterOfPosition(node.pos).line + 1}`, classes, parent: '' })
        }
        ts.forEachChild(node, walk)
      }
      walk(source)
    }
  }
  visitDirectory(path.join(process.cwd(), 'src/components'))
  for (const parent of ['meeting-attendee-chip', 'secondary-update-foot', 'release-note-author']) result.push({ label: parent, classes: [], parent })
  return result
}

test('every avatar consumer has a size, and arbitrary initials fit its bounds', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/agent`)
  await expect(page.locator('.app')).toBeVisible()
  const inventory = consumers()
  expect(inventory.length).toBeGreaterThan(100)
  await page.evaluate(async inventory => {
    const modulePath = '/e2e/avatar-consumers-fixture.tsx'
    const fixture = await import(/* @vite-ignore */ modulePath)
    fixture.mount(inventory)
    await document.fonts.ready
  }, inventory)
  await expect(page.locator('[data-consumer]')).toHaveCount(inventory.length * 4)
  await expect.poll(() => page.locator('#avatar-consumer-fixture [data-user-avatar]').evaluateAll(avatars => avatars.filter(avatar => {
    const bounds = avatar.getBoundingClientRect()
    return bounds.width <= 0 || bounds.height <= 0
  }).map(avatar => avatar.closest('[data-consumer]')?.getAttribute('data-consumer')))).toEqual([])
  await expect.poll(() => page.locator('[data-consumer] .user-avatar-initials').evaluateAll(initials => initials.filter(initial => {
    const bounds = initial.getBoundingClientRect(), slot = initial.parentElement!.getBoundingClientRect()
    return bounds.left < slot.left - 0.02 || bounds.right > slot.right + 0.02 || bounds.top < slot.top - 0.02 || bounds.bottom > slot.bottom + 0.02
  }).map(initial => `${initial.closest('[data-consumer]')?.getAttribute('data-consumer')}: ${initial.textContent}`))).toEqual([])
  await expect.poll(() => page.locator('[data-fit] .user-avatar-initials,[data-fit] .workspace-initials').evaluateAll(initials => initials.filter(initial => {
    const bounds = initial.getBoundingClientRect(), slot = (initial.closest('.workspace-avatar') ?? initial.parentElement!).getBoundingClientRect()
    return bounds.left < slot.left - 0.02 || bounds.right > slot.right + 0.02 || bounds.top < slot.top - 0.02 || bounds.bottom > slot.bottom + 0.02
  }).map(initial => initial.textContent))).toEqual([])
  for (const [parent, size] of [['meeting-attendee-chip', 18], ['secondary-update-foot', 20], ['release-note-author', 24]] as const) {
    await expect(page.locator(`[data-consumer="${parent}"] [data-user-avatar]`).first()).toHaveCSS('width', `${size}px`)
  }
})

test('Latin initials take the reference font size for their avatar size and inherit their weight', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/agent`)
  await expect(page.locator('.app')).toBeVisible()
  const inventory = consumers()
  await page.evaluate(async inventory => {
    const modulePath = '/e2e/avatar-consumers-fixture.tsx'
    const fixture = await import(/* @vite-ignore */ modulePath)
    fixture.mount(inventory)
    await document.fonts.ready
  }, inventory)
  await expect(page.locator('[data-consumer]')).toHaveCount(inventory.length * 4)
  // The reference's avatar tiers (read from its avatar component and measured live at 1440): 14px micro, 16px tiny, 18px small, 24px regular,
  // 32px large. Each gives a font size for two letters ("SA"), for exactly "WW" and for three letters ("FOA"). A 20px avatar has no tier of
  // its own and reads like 18px.
  const reference: Record<number, Record<string, string>> = {
    14: { two: '8px', ww: '5px', three: '4px' },
    16: { two: '9px', ww: '6px', three: '5px' },
    18: { two: '9px', ww: '7px', three: '5px' },
    20: { two: '9px', ww: '7px', three: '5px' },
    24: { two: '11px', ww: '9px', three: '7px' },
    32: { two: '14px', ww: '12px', three: '9px' },
  }
  for (const [kind, count] of [['two', '2'], ['ww', 'ww'], ['three', '3']] as const) {
    const selector = `[data-consumer] [data-user-avatar][data-script="latin"][data-initials="${count}"]`
    await expect.poll(() => page.locator(selector).evaluateAll((avatars, { reference, kind }) => avatars.flatMap(avatar => {
      const width = avatar.getBoundingClientRect().width, size = reference[width]?.[kind]
      if (!size) return []
      const text = avatar.querySelector('.user-avatar-initials') as HTMLElement, initials = getComputedStyle(text), parent = getComputedStyle(avatar.parentElement!)
      const consumer = avatar.closest('[data-consumer]')?.getAttribute('data-consumer')
      // The reference never sets a weight on the avatar: the initials sit at the weight of the row they are in. Document thread avatars
      // set 400 on purpose (the reference's document comment avatars measure 400 inside a 450 card), and so do the owner avatars of the team documents list.
      // The triage list creator avatar (9px/400) and the coding session prompt avatar (8px/400) sit in 16px/400 wrappers in the reference
      // (desktop-ref issues-list/triage-ref.json, agent/session-chat-ref.json), while the fixture row here is the 450 body.
      const at400 = ['document-thread-avatar', 'team-docs-avatar', 'flow-triage-row__avatar', 'coding-session-prompt-avatar'].some(name => avatar.classList.contains(name))
      const inherits = initials.fontWeight === parent.fontWeight || at400 && initials.fontWeight === '400'
      // The size is the table's, never a scaled-down fit, and the letters stay inside the avatar.
      const bounds = text.getBoundingClientRect(), slot = avatar.getBoundingClientRect()
      const inside = bounds.left >= slot.left - 0.02 && bounds.right <= slot.right + 0.02 && bounds.top >= slot.top - 0.02 && bounds.bottom <= slot.bottom + 0.02
      return initials.fontSize === size && inherits && inside && !text.style.transform ? [] : [`${consumer}: ${width}px ${text.textContent} ${initials.fontSize}/${initials.fontWeight} (row ${parent.fontWeight}) inside ${inside} transform "${text.style.transform}"`]
    }), { reference, kind })).toEqual([])
    // Every size of the table is exercised by at least one consumer.
    const widths = await page.locator(selector).evaluateAll(avatars => [...new Set(avatars.map(avatar => avatar.getBoundingClientRect().width))])
    for (const size of Object.keys(reference)) expect(widths, `${kind} initials at ${size}px`).toContain(Number(size))
  }
})

test('meeting attendee avatars keep their initials beside the name in every layout', async ({ page, request, workspaceKey }) => {
  test.setTimeout(180_000)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const boot = await (await request.get('/api/bootstrap', { headers })).json()
  const response = await request.post('/api/meetings', { headers, data: { title: 'Weekly sync', startsAt: '2026-11-03T10:00:00Z', durationMinutes: 30, attendeeIds: [boot.viewer.id] } })
  expect(response.status()).toBe(201)
  const meeting = await response.json()
  let name = ''
  await page.route('**/api/bootstrap*', async route => {
    const response = await route.fetch(), data = await response.json()
    for (const user of [data.viewer, ...data.users]) if (user.id === boot.viewer.id) user.displayName = user.name = name
    await route.fulfill({ response, json: data })
  })
  try {
    for (const width of [1440, 375, 390]) for (const theme of ['Dark', 'Light']) for (const locale of ['en-US', 'zh-CN']) {
      name = locale === 'zh-CN' ? '中 文' : 'William Worthington-Williams'
      await request.patch('/api/account/settings', { headers, data: { interfaceTheme: theme, language: locale } })
      await page.setViewportSize({ width, height: width === 1440 ? 900 : width === 375 ? 667 : 844 })
      await page.goto(`/${workspaceKey}/meeting/${meeting.id}`)
      const chip = page.locator('.meeting-attendee-chip').first(), avatar = chip.locator('[data-user-avatar]')
      await expect(chip).toContainText(name)
      await expect(avatar).toHaveCSS('width', '18px')
      await expect(avatar).toHaveCSS('height', '18px')
      await page.evaluate(() => document.fonts.ready)
      await expect.poll(() => chip.evaluate(element => {
        const avatar = element.querySelector('[data-user-avatar]')!.getBoundingClientRect()
        const text = element.querySelector('.user-avatar-initials')!.getBoundingClientRect()
        const label = element.querySelector(':scope > span:last-child')!.getBoundingClientRect()
        return text.left >= avatar.left - 0.02 && text.right <= avatar.right + 0.02 && label.left >= avatar.right
      })).toBe(true)
      if (process.env.FLOW_REVIEW_SHOTS) await chip.screenshot({ path: `${process.env.FLOW_REVIEW_SHOTS}/meeting-avatar-${width}-${theme}-${locale}.png` })
    }
  } finally {
    await request.patch('/api/account/settings', { headers, data: { interfaceTheme: 'System preference', language: 'en-US' } })
  }
})
