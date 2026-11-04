import { expect, type Locator } from '@playwright/test'
import { test } from './workspace-fixture'
import { bootstrap, preferences } from './review-findings-fixture'

for (const width of [375, 390, 1440]) for (const theme of ['Dark', 'Light'] as const) for (const locale of ['en-US', 'zh-CN']) {
  test(`contains settings, project names and issue pills ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
    test.setTimeout(90_000)
    await page.setViewportSize({ width, height: width === 1440 ? 900 : width === 375 ? 667 : 844 })
    await preferences(request, workspaceKey, theme, locale)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const data = await bootstrap(request, workspaceKey)
    expect((await request.patch('/api/account/profile', { headers, data: { displayName: '中 文', username: 'reviewer' } })).ok()).toBe(true)
    const team = data.teams[0]
    const longName = '中文 International infrastructure reliability and launch dependencies across regional teams'
    expect((await request.patch(`/api/workspaces/${workspaceKey}/teams/${team.id}`, { headers, data: { name: longName.slice(0, 48) } })).ok()).toBe(true)
    const created = await request.post('/api/projects', { headers, data: { name: longName, teamIds: [team.id] } })
    expect(created.status()).toBe(201)
    const project = await created.json()
    const createIssue = async (title: string, extra = {}) => {
      const response = await request.post('/api/issues', { headers, data: { title, teamId: team.id, stateId: data.states.find(state => state.type === 'unstarted')!.id, assigneeId: data.viewer.id, ...extra } })
      expect(response.status()).toBe(201)
      return response.json()
    }
    const parent = await createIssue(longName)
    const child = await createIssue('中文 Launch', { parentId: parent.id, labelIds: [data.labels[0].id] })
    let withSession = false
    // Live replay must not replace the scripted session with the stored issue.
    await page.route('**/api/realtime/events?**', route => route.fulfill({ contentType: 'text/event-stream', body: ': fixture\n\n' }))
    await page.route(/\/api\/(?:issue-records(?:\/bootstrap)?|bootstrap)(?:\?|$)/, async route => {
      const response = await route.fetch()
      const body = await response.json()
      if (withSession) for (const issue of [...(body.items ?? []), ...(body.descendants ?? []), ...(body.issues ?? [])]) {
        if (issue.id === child.id) {
          issue.delegate = { ...data.viewer, id: 'fixture-agent', name: '中 文', displayName: '中 文', app: true }
          issue.agentSessionState = 'error'
        }
      }
      await route.fulfill({ response, json: body })
    })
    const shots = process.env.FLOW_CI_SHOTS
    const shot = async (name: string, region?: Locator) => {
      if (shots && (width === 390 && theme === 'Dark' && locale === 'en-US' || width === 375 && theme === 'Light' && locale === 'zh-CN' || width === 1440 && locale === 'en-US' && name === 'home-menu')) await (region ?? page).screenshot({ path: `${shots}/${name}-${width}-${theme}-${locale}.png`, animations: 'disabled' })
    }

    await page.goto(`/${workspaceKey}/settings/teams`)
    const filter = page.locator('.settings-members-toolbar input')
    await expect(filter).toBeVisible()
    // The phone teams table keeps the reference's 504px scrolling layout; the phone gate measures its geometry.
    if (width < 768) await shot('teams')
    await filter.hover()
    await page.keyboard.press('Tab')
    await filter.focus()
    expect(await filter.evaluate(element => element.matches(':focus-visible'))).toBe(true)
    if (width < 768) await shot('teams-focus', filter.locator('..'))
    await filter.fill('No matching team')
    await expect(page.locator('.mobile-team-row,.settings-teams-row')).toHaveCount(0)
    await filter.fill('')

    await page.goto(`/${workspaceKey}/settings/api`)
    const issued = page.locator('.api-keys-section .personal-row-link')
    await expect(issued).toBeVisible()
    expect(await issued.evaluate(element => [...element.children].every(child => child.getBoundingClientRect().right <= element.getBoundingClientRect().right))).toBe(true)
    await issued.scrollIntoViewIfNeeded()
    if (width < 768) await shot('api')
    await issued.hover()
    await page.keyboard.press('Tab')
    await issued.focus()
    expect(await issued.evaluate(element => element.matches(':focus-visible'))).toBe(true)
    if (width < 768) await shot('api-focus', issued)
    await issued.click()
    await expect(page).toHaveURL(new RegExp('/settings/api/keys$'))

    await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
    const title = page.getByRole('textbox', { name: locale === 'zh-CN' ? '项目名称' : 'Project name', exact: true })
    await expect(title).toBeVisible()
    if (width < 768) {
      const closeDetails = page.getByRole('button', { name: /Close project details|关闭项目详情/ })
      if (await closeDetails.count()) await closeDetails.click()
    }
    expect(await title.evaluate(element => element.scrollHeight <= element.clientHeight && element.scrollWidth <= element.clientWidth)).toBe(true)
    await title.hover()
    await shot('project-hover', title)
    await title.focus()
    await shot('project-focus', title)
    await title.fill('中文')
    await Promise.all([page.waitForResponse(response => response.url().endsWith(`/api/projects/${project.id}`) && response.request().method() === 'PATCH'), title.press('Enter')])
    await expect(title).toHaveValue('中文')
    await title.fill(longName)
    await Promise.all([page.waitForResponse(response => response.url().endsWith(`/api/projects/${project.id}`) && response.request().method() === 'PATCH'), title.press('Enter')])
    await expect(title).toHaveValue(longName)
    await shot('project', title)

    for (const route of ['my-issues/assigned', `team/${team.key}/all`]) {
      await page.goto(`/${workspaceKey}/${route}`)
      const row = page.locator(`[data-issue-row-id="${child.id}"]`)
      await expect(row).toBeVisible()
      expect(await page.locator('[data-issue-row-id] .issue-assignee-cluster').evaluateAll(elements => elements.every(element => {
        const bounds = element.closest('[data-issue-row-id]')!.getBoundingClientRect()
        const box = element.getBoundingClientRect()
        if (box.left < bounds.left || box.right > bounds.right) return false
        for (let parent = element.parentElement; parent && !parent.hasAttribute('data-issue-row-id'); parent = parent.parentElement) {
          if (getComputedStyle(parent).overflowX === 'hidden' && box.right > parent.getBoundingClientRect().right) return false
        }
        return true
      }))).toBe(true)
      const badge = row.locator('[class*=badgeSlot] > [class*=badge]').first()
      await expect(badge).toBeVisible()
      expect(await badge.evaluate(element => element.getBoundingClientRect().right <= element.parentElement!.getBoundingClientRect().right)).toBe(true)
      await row.hover()
      await row.focus()
      if (width < 768 && route.startsWith('team')) await shot('issue-row')
      await badge.click()
      await expect(page.getByRole('dialog').last()).toBeVisible()
      if (width < 768 && route.startsWith('team')) await shot('row-label-open', page.getByRole('dialog').last())
      await page.keyboard.press('Escape')
    }
    if (width < 768) {
      const labelResponse = await request.post('/api/labels', { headers, data: { name: longName.slice(0, 48), color: '#4cb782' } })
      expect(labelResponse.status()).toBe(201)
      const label = await labelResponse.json()
      expect((await request.patch(`/api/issues/${child.id}`, { headers, data: { labelIds: [label.id] } })).ok()).toBe(true)
      withSession = true
      for (const route of ['my-issues/assigned', `team/${team.key}/all`]) {
        await page.goto(`/${workspaceKey}/${route}`)
        const row = page.locator(`[data-issue-row-id="${child.id}"]`)
        const session = row.locator('[data-session-pill]')
        await expect(session).toBeVisible()
        const box = (await session.boundingBox())!
        expect(box.x + box.width).toBeLessThanOrEqual((await row.boundingBox())!.width)
        if (route.startsWith('team')) await shot('session-row')
      }
    }
    await page.goto(`/${workspaceKey}/issue/${child.identifier}`)
    if (width < 768) {
      const properties = page.locator('.issue-mobile-properties')
      const projectPill = properties.locator('.issue-project-trigger')
      await expect(projectPill).toBeVisible()
      const fills = await properties.evaluate(element => {
        const project = element.querySelector('.issue-project-trigger')!
        const status = element.querySelector('.core-property-trigger')!
        return [getComputedStyle(project).backgroundColor, getComputedStyle(status).backgroundColor]
      })
      expect(fills[0]).toBe(fills[1])
      await shot('issue-properties', properties)
      expect((await request.patch(`/api/issues/${child.id}`, { headers, data: { projectId: project.id } })).ok()).toBe(true)
      await page.reload()
      await expect(projectPill).toContainText(longName)
      await projectPill.hover()
      await projectPill.focus()
      await shot('project-pill-selected', properties)
      await projectPill.click()
      await expect(page.getByRole('dialog').last()).toBeVisible()
      await page.keyboard.press('Escape')
    } else {
      await page.goto(`/${workspaceKey}/settings/account/preferences`)
      const home = page.getByRole('combobox', { name: locale === 'zh-CN' ? '默认主页视图' : 'Default home view' })
      await home.click()
      const suffix = page.locator('.settings-select-option-content .settings-select-suffix')
      await expect(suffix).toBeVisible()
      expect(await suffix.evaluate(element => getComputedStyle(element).color !== getComputedStyle(element.parentElement!).color)).toBe(true)
      await shot('home-menu')
    }
  })
}
