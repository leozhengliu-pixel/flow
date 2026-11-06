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
  await expect(page.locator('[data-consumer]')).toHaveCount(inventory.length * 2)
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
  await expect(page.locator('[data-consumer]')).toHaveCount(inventory.length * 2)
  // Measured on the reference at 1440 and on phones: 14px -> 8px, 16px and 18px -> 9px, 24px -> 11px, 32px -> 14px (20px reads 9px between them).
  const reference: Record<number, string> = { 14: '8px', 16: '9px', 18: '9px', 20: '9px', 24: '11px', 32: '14px' }
  await expect.poll(() => page.locator('[data-consumer] [data-user-avatar][data-script="latin"]').evaluateAll((avatars, reference) => avatars.flatMap(avatar => {
    const width = avatar.getBoundingClientRect().width, size = reference[width]
    if (!size) return []
    const initials = getComputedStyle(avatar.querySelector('.user-avatar-initials')!), parent = getComputedStyle(avatar.parentElement!)
    const consumer = avatar.closest('[data-consumer]')?.getAttribute('data-consumer')
    // The reference never sets a weight on the avatar: the initials sit at the weight of the row they are in. Document thread avatars
    // set 400 on purpose (the reference's document comment avatars measure 400 inside a 450 card).
    const inherits = initials.fontWeight === parent.fontWeight || avatar.classList.contains('document-thread-avatar') && initials.fontWeight === '400'
    return initials.fontSize === size && inherits ? [] : [`${consumer}: ${width}px ${initials.fontSize}/${initials.fontWeight} (row ${parent.fontWeight})`]
  }), reference)).toEqual([])
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
