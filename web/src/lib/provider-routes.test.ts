import { expect, it } from 'vitest'
import {
  enabledIntegrationsSettingsPath,
  integrationSettingsPath,
  jiraSyncEditPath,
  jiraSyncNewPath,
  parseAppRoute,
  type IntegrationProvider,
} from './app-routes'

it('round-trips every supported provider through a persistent settings route', () => {
  const providers: IntegrationProvider[] = ['github', 'gitlab', 'jira']
  for (const provider of providers) {
    const path = integrationSettingsPath('workspace', provider)
    expect(parseAppRoute(path)).toEqual({
      kind: 'settings',
      workspaceSlug: 'workspace',
      page: 'integrations',
      integrationProvider: provider,
      integrationSlug: provider,
    })
  }
})

it('resolves catalog slug shells and the enabled integrations page', () => {
  expect(parseAppRoute('/workspace/settings/integrations/enabled')).toEqual({
    kind: 'settings',
    workspaceSlug: 'workspace',
    page: 'integrations',
    integrationSlug: 'enabled',
  })
  expect(enabledIntegrationsSettingsPath('workspace')).toBe(
    '/workspace/settings/integrations/enabled',
  )
  for (const slug of ['figma', 'sentry', 'notion', 'zapier']) {
    expect(parseAppRoute(`/workspace/settings/integrations/${slug}`)).toEqual({
      kind: 'settings',
      workspaceSlug: 'workspace',
      page: 'integrations',
      integrationSlug: slug,
      integrationProvider: undefined,
    })
  }
})

it('resolves jira sync wizard routes', () => {
  expect(parseAppRoute(jiraSyncNewPath('workspace'))).toEqual({
    kind: 'settings',
    workspaceSlug: 'workspace',
    page: 'integrations',
    integrationProvider: 'jira',
    integrationSlug: 'jira',
    jiraSyncMode: 'new',
  })
  expect(parseAppRoute(jiraSyncEditPath('workspace', '10000'))).toEqual({
    kind: 'settings',
    workspaceSlug: 'workspace',
    page: 'integrations',
    integrationProvider: 'jira',
    integrationSlug: 'jira',
    jiraSyncMode: 'edit',
    jiraProjectId: '10000',
  })
})
