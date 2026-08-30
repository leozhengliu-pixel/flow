import { describe, expect, it } from 'vitest'
import type { BootstrapData } from '@/types/flow'
import { deriveResourceCounts } from './resource-counts'

describe('resource counts', () => {
  it('counts active issue, project, and initiative relationships without duplicates', () => {
    const label = { id: 'label-1', name: 'Product', color: '#123456', resourceType: 'issue', issueCount: 99 }
    const projectLabel = { id: 'label-2', name: 'Portfolio', color: '#654321', resourceType: 'project', issueCount: 99 }
    const data = {
      labels: [label, projectLabel],
      issues: [
        { id: 'issue-1', labels: [label, label], project: { id: 'project-1' } },
        { id: 'issue-2', labels: [label], project: { id: 'project-1' }, archivedAt: '2026-08-01T00:00:00.000Z' },
      ],
      projects: [
        { id: 'project-1', labelIds: ['label-2', 'label-2'] },
        { id: 'project-2', labelIds: ['label-2'], archivedAt: '2026-08-01T00:00:00.000Z' },
      ],
      initiatives: [{ id: 'initiative-1', labelIds: ['label-2'] }],
    } as unknown as BootstrapData

    const result = deriveResourceCounts(data)
    expect(result.labels.find(item => item.id === 'label-1')?.issueCount).toBe(1)
    expect(result.labels.find(item => item.id === 'label-2')?.issueCount).toBe(2)
    expect(result.projects.find(item => item.id === 'project-1')?.issueCount).toBe(1)
    expect(result.projects.find(item => item.id === 'project-2')?.issueCount).toBe(0)
    expect(result.issues[0].labels[0].issueCount).toBe(1)
  })
})
