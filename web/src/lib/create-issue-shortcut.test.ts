import { expect, it } from 'vitest'
import { parseAppRoute } from '@/lib/app-routes'
import { makeBootstrap, project } from '@/test/fixtures'
import { createIssueShortcutContext } from './create-issue-shortcut'

const data = makeBootstrap({ projects: [{ ...project, teamIds: ['team-1'] }] })

it('prefills the current project on every project detail tab and saved project view', () => {
  for (const path of ['/acme/project/project-one/overview', '/acme/project/project-one/issues', '/acme/project/project-one/activity']) {
    const route = parseAppRoute(path)
    expect(route.kind).toBe('project')
    expect(createIssueShortcutContext(route, data)).toEqual({ projectId: project.id, teamId: 'team-1' })
  }
  expect(createIssueShortcutContext({ kind: 'project-saved-view', workspaceSlug: 'acme', projectSlugId: 'project-one', viewId: 'view-1' }, data)).toEqual({ projectId: project.id, teamId: 'team-1' })
})

it('keeps the plain create flow outside project pages or for unknown projects', () => {
  expect(createIssueShortcutContext(parseAppRoute('/acme/projects/all'), data)).toBeUndefined()
  expect(createIssueShortcutContext({ kind: 'project', workspaceSlug: 'acme', projectSlugId: 'missing', tab: 'overview' }, data)).toBeUndefined()
  expect(createIssueShortcutContext({ kind: 'project', workspaceSlug: 'acme', projectSlugId: 'project-one', tab: 'overview' }, null)).toBeUndefined()
})
