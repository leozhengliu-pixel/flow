import { expect, it } from 'vitest'
import {
  availabilityLabel,
  getIntegrationCatalogEntry,
  getIntegrationProductPattern,
  isInstalledIntegrationConnection,
  primaryIntegrations,
  subtypesFor,
} from './integration-catalog'

it('exposes primary catalog entries with honest availability', () => {
  const entries = primaryIntegrations()
  expect(entries.some((item) => item.slug === 'github' && item.availability === 'supported')).toBe(true)
  expect(entries.some((item) => item.slug === 'jira' && item.availability === 'supported' && item.connectProvider === 'jira')).toBe(true)
  expect(entries.filter((item) => item.connectProvider === 'feishu' || item.connectProvider === 'lark').map((item) => item.slug)).toEqual(['feishu', 'lark'])
  expect(entries.some((item) => item.slug === 'salesforce' && item.availability === 'not_supported')).toBe(true)
  expect(availabilityLabel('coming_soon')).toBe('Coming soon')
})

it('keeps pending or unhealthy chat applications available for management', () => {
  expect(isInstalledIntegrationConnection({ provider: 'feishu', status: 'pending_authorization' })).toBe(true)
  expect(isInstalledIntegrationConnection({ provider: 'lark', status: 'error' })).toBe(true)
  expect(isInstalledIntegrationConnection({ provider: 'feishu', status: 'disconnected' })).toBe(false)
  expect(isInstalledIntegrationConnection({ provider: 'slack', status: 'error' })).toBe(false)
})

it('keeps subtypes out of the primary grid and nested under parents', () => {
  expect(primaryIntegrations().every((item) => !item.subtype)).toBe(true)
  expect(subtypesFor('github').map((item) => item.service)).toContain('githubCodeAccessPersonal')
  expect(getIntegrationCatalogEntry('github-code-access-personal')?.parentSlug).toBe('github')
})

it('exposes coming-soon product patterns for long-tail OAuth (P1-D)', () => {
  const intercom = getIntegrationCatalogEntry('intercom')!
  const pattern = getIntegrationProductPattern(intercom)
  expect(pattern?.headline).toMatch(/Conversations/i)
  expect(pattern?.capabilities.length).toBeGreaterThan(0)
  expect(getIntegrationCatalogEntry('github-enterprise-cloud')?.parentSlug).toBe('github')
  expect(getIntegrationProductPattern(getIntegrationCatalogEntry('github')!)).toBeUndefined()
})
