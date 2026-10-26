import { describe, expect, it } from 'vitest'

// App.tsx is too large to render in a unit test, so guard the create-issue entries at
// the source level: none may pick a status from the workspace-wide list, because the
// API only accepts a status from the target team's workflow.
async function appSource() {
  const fs = (await import(/* @vite-ignore */ `node:${'fs'}`)) as { readFileSync: (path: string, encoding: 'utf8') => string }
  const cwd = (globalThis as unknown as { process: { cwd: () => string } }).process.cwd()
  return fs.readFileSync(`${cwd}/src/App.tsx`, 'utf8')
}

function block(source: string, start: string, end: string) {
  const from = source.indexOf(start)
  expect(from).toBeGreaterThanOrEqual(0)
  return source.slice(from, source.indexOf(end, from))
}

describe('App create-issue wiring', () => {
  it('opens the composer without a workspace-wide default status', async () => {
    const open = block(await appSource(), 'const openCreateIssue = useCallback(', 'usePrefilledIssueRoute(')
    expect(open).not.toMatch(/\.states\b/)
  })

  it('project and triage entries resolve the team and status from the team workflow', async () => {
    const source = await appSource()
    expect(block(source, 'onCreateIssue={(projectId, projectMilestoneId, context) => {', 'onOpenSidebar=')).toContain('openCreateIssue(projectCreateIssueContext(data, projectId, projectMilestoneId, context))')
    expect(source).toContain('teamWorkflowStateOfType(data, triageTeam.id, "backlog")')
  })
})
