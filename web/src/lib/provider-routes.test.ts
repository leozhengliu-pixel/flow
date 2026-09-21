import { expect, it } from 'vitest'
import {
  enabledIntegrationsSettingsPath,
  integrationSettingsPath,
  parseAppRoute,
  type IntegrationProvider,
} from './app-routes'

it('round-trips every supported provider through a persistent settings route', () => {
  const providers: IntegrationProvider[] = ['github', 'gitlab']
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
  for (const slug of ['jira', 'figma', 'sentry', 'notion', 'zapier']) {
    expect(parseAppRoute(`/workspace/settings/integrations/${slug}`)).toEqual({
      kind: 'settings',
      workspaceSlug: 'workspace',
      page: 'integrations',
      integrationSlug: slug,
      integrationProvider: undefined,
    })
  }
})
