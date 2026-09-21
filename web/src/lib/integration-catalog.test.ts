import { expect, it } from 'vitest'
import {
  availabilityLabel,
  getIntegrationCatalogEntry,
  primaryIntegrations,
  subtypesFor,
} from './integration-catalog'

it('exposes primary catalog entries with honest availability', () => {
  const entries = primaryIntegrations()
  expect(entries.some((item) => item.slug === 'github' && item.availability === 'supported')).toBe(true)
  expect(entries.some((item) => item.slug === 'jira' && item.availability === 'coming_soon')).toBe(true)
  expect(entries.some((item) => item.slug === 'salesforce' && item.availability === 'not_supported')).toBe(true)
  expect(availabilityLabel('coming_soon')).toBe('Coming soon')
})

it('keeps subtypes out of the primary grid and nested under parents', () => {
  expect(primaryIntegrations().every((item) => !item.subtype)).toBe(true)
  expect(subtypesFor('github').map((item) => item.service)).toContain('githubCodeAccessPersonal')
  expect(getIntegrationCatalogEntry('github-code-access-personal')?.parentSlug).toBe('github')
})
