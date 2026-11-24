import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'
import type { Initiative, Project } from '../src/types/flow'
import { test } from './workspace-fixture'

const projectCount = 110
const day = (offset: number) => new Date(Date.now() + offset * 86_400_000).toISOString().slice(0, 10)
const projectName = (index: number) => `Timeline project ${String(index).padStart(3, '0')}`

async function seed(request: APIRequestContext, workspaceKey: string) {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap?projection=project-list', { headers })
  expect(bootstrap.ok()).toBeTruthy()
  const { teams: [team] } = await bootstrap.json()
  const projects: Project[] = []
  for (let index = 0; index < projectCount; index++) {
    const offset = index % 3 === 0 ? (index - 55) * 135 : index % 17
    const response = await request.post('/api/projects', { headers, data: {
      name: projectName(index),
      teamIds: [team.id],
      startDate: day(offset - 45),
      targetDate: day(offset + 45),
      milestoneDetails: Array.from({ length: 12 }, (_, milestone) => ({
        name: `Milestone ${index}.${milestone}`, targetDate: day(offset - 36 + milestone * 6),
      })),
      dependencyIds: projects.slice(-6).map(project => project.id),
    } })
    expect(response.status()).toBe(201)
    projects.push(await response.json())
  }
  const roadmap = await request.post('/api/initiatives', { headers, data: {
    name: 'Timeline roadmap', projectIds: projects.map(project => project.id),
  } })
  expect(roadmap.status()).toBe(201)
  for (let index = 0; index < 48; index++) {
    const project = projects[index % 8]
    const issue = await request.post('/api/issues', { headers, data: {
      teamId: team.id, title: `Timeline issue ${index}`, projectId: project.id,
      projectMilestoneId: project.milestones?.[index % 12]?.id,
    } })
    expect(issue.status()).toBe(201)
    if (index === 0) {
      const { id } = await issue.json()
      const comment = await request.post(`/api/issues/${id}/comments`, { headers, data: { body: 'Timeline issue history' } })
      expect(comment.status()).toBe(201)
    }
  }
  return { roadmap: await roadmap.json() as Initiative, project: projects[0] }
}

async function measure(page: Page, session: string, operation: () => Promise<void>) {
  const end = await page.evaluateHandle(() => {
    let task = 0
    let frame = 0
    let animation = 0
    let previous = performance.now()
    const sample = (now: number) => {
      frame = Math.max(frame, now - previous)
      previous = now
      animation = requestAnimationFrame(sample)
    }
    animation = requestAnimationFrame(sample)
    const supportsLongTasks = PerformanceObserver.supportedEntryTypes?.includes('longtask')
    const observe = (entries: PerformanceEntry[]) => {
      for (const entry of entries) task = Math.max(task, entry.duration)
    }
    const observer = supportsLongTasks ? new PerformanceObserver(list => observe(list.getEntries())) : undefined
    observer?.observe({ type: 'longtask' })
    return async () => {
      await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
      observe(observer?.takeRecords() ?? [])
      observer?.disconnect()
      cancelAnimationFrame(animation)
      return { task, frame }
    }
  })
  await operation()
  const metrics = await end.evaluate(finish => finish())
  await end.dispose()
  // 200 ms catches a visible stall. Allow 50 ms of extra scheduling jitter
  // when using frame gaps as the fallback in engines without Long Tasks.
  expect(metrics.task, `${session}: longest main-thread task (ms)`).toBeLessThanOrEqual(200)
  expect(metrics.frame, `${session}: longest frame gap (ms)`).toBeLessThanOrEqual(250)
}

async function scrollWindows(scroller: Locator) {
  return scroller.evaluate(async node => {
    const waitForPaint = () => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
    const maxTop = node.scrollHeight - node.clientHeight
    const maxLeft = node.scrollWidth - node.clientWidth
    let peakNodes = node.querySelectorAll('*').length
    let peakRows = 0
    for (let pass = 0; pass < 3; pass++) {
      for (const fraction of [0, .25, .5, .75, 1, .75, .25, 0]) {
        node.scrollTop = maxTop * fraction
        node.scrollLeft = maxLeft * fraction
        await waitForPaint()
        peakNodes = Math.max(peakNodes, node.querySelectorAll('*').length)
        peakRows = Math.max(peakRows, node.querySelectorAll('.lp-project-timeline__row, .li-roadmap-row').length)
      }
    }
    return { peakNodes, peakRows, maxTop, maxLeft }
  })
}

test.use({ viewport: { width: 1440, height: 900 } })
test.setTimeout(180_000)

test('opens and scrolls large project and initiative timelines within a bounded viewport', async ({ page, request, workspaceKey, browserName }) => {
  const { roadmap, project } = await seed(request, workspaceKey)
  await page.addInitScript(key => {
    localStorage.setItem('flow:locale', 'en-US')
    localStorage.setItem(`flow:projects:view:${key}:workspace:all`, JSON.stringify({
      grouping: 'No grouping', groupOrder: 'asc', layout: 'list', ordering: 'Name', orderingDirection: 'asc',
      properties: ['Priority', 'Status', 'Health', 'Lead'], showClosed: 'All', showEmptyGroups: false, timelineZoom: 'week',
    }))
  }, workspaceKey)
  await page.goto(`/${workspaceKey}/projects/all`)
  await expect(page.getByRole('row', { name: /^Timeline project/ }).first()).toBeVisible({ timeout: 30_000 })
  await page.getByRole('button', { name: 'Display options', exact: true }).click()
  const timeline = page.getByRole('region', { name: 'Project timeline', exact: true })
  const scroller = timeline.locator('.lp-project-timeline__scroller')
  await measure(page, 'open project timeline', async () => {
    await page.getByRole('tab', { name: 'Timeline', exact: true }).click()
    await expect(timeline.locator('.lp-project-timeline__row').first()).toBeVisible()
  })
  await page.keyboard.press('Escape')
  await measure(page, 'load the second project page', async () => {
    const more = page.getByRole('button', { name: 'Load more projects', exact: true })
    await more.click()
    await expect(more).toHaveCount(0)
  })
  const client = browserName === 'chromium' ? await page.context().newCDPSession(page) : undefined
  const heap = async () => {
    await client?.send('HeapProfiler.collectGarbage')
    return (await client?.send('Runtime.getHeapUsage'))?.usedSize ?? 0
  }
  const before = await heap()
  await measure(page, 'scroll project timeline', async () => {
    const result = await scrollWindows(scroller)
    expect(result.maxTop).toBeGreaterThan(6_000)
    expect(result.maxLeft).toBeGreaterThan(250_000)
    expect(result.peakRows).toBeLessThan(32)
    expect(result.peakNodes).toBeLessThan(3_500)
  })
  // Allow one viewport's transient React and browser allocations after GC.
  if (client) {
    expect(await heap() - before).toBeLessThan(8 * 1024 ** 2)
    await client.detach()
  }
  await measure(page, 'zoom project timeline and return to today', async () => {
    await timeline.getByRole('button', { name: 'Timeline zoom', exact: true }).click()
    await page.getByRole('menuitemradio', { name: 'Quarter', exact: true }).click()
    await timeline.getByRole('button', { name: 'Center timeline on today', exact: true }).click()
    await expect(timeline).toHaveAttribute('data-zoom', 'quarter')
  })
  await scroller.evaluate(node => { node.scrollTop = node.scrollHeight })
  const last = timeline.getByRole('button', { name: `Open ${projectName(projectCount - 1)}`, exact: true })
  await expect(last).toBeInViewport()
  await last.focus()
  await scroller.evaluate(node => { node.scrollTop = 0 })
  await expect(timeline.getByRole('button', { name: `Open ${projectName(0)}`, exact: true })).toBeInViewport()
  await expect(last).toBeFocused()

  await page.goto(`/${workspaceKey}/initiative/${roadmap.slugId}/overview`)
  const projectsTab = page.locator('.li-detail-toolbar').getByRole('link', { name: 'Projects', exact: true })
  await expect(projectsTab).toBeVisible({ timeout: 30_000 })
  const initiative = page.locator('.li-roadmap')
  await measure(page, 'open initiative roadmap', async () => {
    await projectsTab.click()
    await expect(initiative.locator('.li-roadmap-row').first()).toBeVisible()
  })
  await measure(page, 'scroll initiative roadmap', async () => {
    const result = await scrollWindows(initiative)
    expect(result.maxTop).toBeGreaterThan(4_000)
    expect(result.maxLeft).toBeGreaterThan(1_000)
    expect(result.peakRows).toBeLessThan(42)
    expect(result.peakNodes).toBeLessThan(2_500)
  })

  const projectIssues = `/${workspaceKey}/project/${project.slugId}/issues`
  await page.goto(projectIssues)
  const issueTitle = page.getByText('Timeline issue 0', { exact: true }).first()
  await expect(issueTitle).toBeVisible({ timeout: 30_000 })
  let repeatedBootstraps = 0
  page.on('request', request => {
    if (/\/api\/(?:issue-records\/)?bootstrap$/.test(new URL(request.url()).pathname)) repeatedBootstraps++
  })
  await issueTitle.click()
  await expect(page.getByText('Timeline issue history', { exact: true })).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(projectIssues)
  await expect(issueTitle).toBeVisible()
  expect(repeatedBootstraps).toBe(0)
})
