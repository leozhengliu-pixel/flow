import { expect, it } from 'vitest'
import {
  integrationHasCodeAccess,
  organizationHasCodeAccess,
  resolveCodeReviewAccess,
} from './code-access'
import type { BootstrapData, IntegrationConnection } from '@/types/flow'

function data(connections: Partial<IntegrationConnection>[]): Pick<BootstrapData, 'integrationConnections'> {
  return {
    integrationConnections: connections.map((item, index) => ({
      id: item.id ?? `c${index}`,
      provider: item.provider ?? 'github',
      name: item.name ?? 'GitHub',
      status: item.status ?? 'connected',
      scopes: item.scopes ?? [],
      channels: [],
      linkbackEnabled: false,
      deliveryAttempts: 0,
      connectedBy: 'u1',
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
      config: item.config,
    })),
  }
}

it('detects code access from config or scopes', () => {
  expect(integrationHasCodeAccess(data([{ config: { codeAccess: 'true' } }]).integrationConnections[0])).toBe(true)
  expect(integrationHasCodeAccess(data([{ scopes: ['repo'] }]).integrationConnections[0])).toBe(true)
  expect(integrationHasCodeAccess(data([{}]).integrationConnections[0])).toBe(false)
})

it('resolves review access reasons for connect / grant / granted', () => {
  expect(resolveCodeReviewAccess(data([])).reason).toBe('workspace_connection_missing')
  expect(resolveCodeReviewAccess(data([{ provider: 'github', status: 'connected' }])).reason).toBe(
    'workspace_code_access_missing',
  )
  expect(
    resolveCodeReviewAccess(data([{ provider: 'github', status: 'connected', config: { codeAccess: 'true' } }])).reason,
  ).toBe('granted')
  expect(organizationHasCodeAccess(data([{ provider: 'github', config: { codeAccess: 'true' } }]))).toBe(true)
})
