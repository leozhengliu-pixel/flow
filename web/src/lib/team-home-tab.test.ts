import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { rememberTeamHomeTab, teamHomeTab, teamHomeTabOf, teamHomeTabPath, useTeamHomeTab } from './team-home-tab'

describe('team home tab', () => {
  beforeEach(() => localStorage.clear())

  it('remembers the last home tab per team and viewer, starting on Overview', () => {
    expect(teamHomeTab('viewer-1', 'team-1')).toBe('overview')
    rememberTeamHomeTab('viewer-1', 'team-1', 'members')
    rememberTeamHomeTab('viewer-1', 'team-2', 'documents')
    expect(teamHomeTab('viewer-1', 'team-1')).toBe('members')
    expect(teamHomeTab('viewer-1', 'team-2')).toBe('documents')
    expect(teamHomeTab('viewer-2', 'team-1')).toBe('overview')
    localStorage.setItem('flow:team-home-tab:viewer-1', JSON.stringify({ 'team-1': 'bogus' }))
    expect(teamHomeTab('viewer-1', 'team-1')).toBe('overview')
  })

  it('maps team home routes to tabs and tabs to paths, Loops falling back to Overview while off', () => {
    expect(teamHomeTabOf({ kind: 'team-documents', workspaceSlug: 'acme', teamKey: 'ENG' })).toBe('documents')
    expect(teamHomeTabOf({ kind: 'team-issues', workspaceSlug: 'acme', teamKey: 'ENG', view: 'all' })).toBeUndefined()
    expect(teamHomeTabPath('acme', 'ENG', 'overview')).toBe('/acme/team/ENG/overview')
    expect(teamHomeTabPath('acme', 'ENG', 'documents')).toBe('/acme/team/ENG/documents')
    expect(teamHomeTabPath('acme', 'ENG', 'members')).toBe('/acme/team/ENG/members')
    expect(teamHomeTabPath('acme', 'ENG', 'loops')).toBe('/acme/team/ENG/loops')
    expect(teamHomeTabPath('acme', 'ENG', 'loops', false)).toBe('/acme/team/ENG/overview')
  })

  it('updates subscribers when another tab is opened', () => {
    const { result } = renderHook(() => useTeamHomeTab('viewer-1', 'team-1'))
    expect(result.current).toBe('overview')
    act(() => rememberTeamHomeTab('viewer-1', 'team-1', 'loops'))
    expect(result.current).toBe('loops')
  })
})
