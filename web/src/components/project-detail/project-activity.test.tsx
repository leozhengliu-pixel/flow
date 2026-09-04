import type { ComponentProps } from 'react'
import { render, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, viewer } from '@/test/fixtures'
import { ProjectActivity } from './project-activity'

function activityProps() {
  const data = makeBootstrap()
  return {
    activities: [],
    documents: data.documents,
    drafts: [],
    initiatives: [],
    integrationConnections: [],
    issues: [],
    labelGroups: [],
    labels: [],
    onCommentProject: async () => ({}) as never,
    onCommentProjectUpdate: async () => ({}) as never,
    onConvertMilestone: async () => project,
    onCreateMilestone: async () => ({}) as never,
    onCreateReminder: async () => ({}) as never,
    onCreateResource: async () => ({}) as never,
    onCreateSavedView: async () => ({}) as never,
    onCreateUpdate: async () => ({}) as never,
    onDelete: async () => undefined,
    onDeleteIssues: async () => undefined,
    onDeleteMilestone: async () => undefined,
    onDeleteProjectUpdateAttachment: async () => ({}) as never,
    onDeleteResource: async () => undefined,
    onDeleteSavedView: async () => undefined,
    onDeleteUpdate: async () => undefined,
    onMoveMilestone: async () => undefined,
    onOpenIssue: () => undefined,
    onReorderMilestones: async () => [],
    onReactProjectUpdate: async () => ({}) as never,
    onSetSubscriptionEvents: async () => undefined,
    onTabChange: () => undefined,
    onToggleFavorite: async () => undefined,
    onUpdate: async () => project,
    onUpdateIssue: async () => ({}) as never,
    onUpdateMilestone: async () => ({}) as never,
    onUpdateProjectUpdate: async () => ({}) as never,
    onUpdateResource: async () => ({}) as never,
    onUpdateSavedView: async () => ({}) as never,
    onUploadProjectUpdateAttachment: async () => ({}) as never,
    onCreateIssue: () => undefined,
    onDeleteProjectUpdate: async () => undefined,
    onOpenMilestoneIssues: () => undefined,
    onOpenSavedView: () => undefined,
    onEditSavedView: () => undefined,
    onCreateProjectUpdate: async () => ({}) as never,
    onUpdateProject: async () => project,
    project,
    projectRelations: [],
    projectStatuses: [project.status],
    projectUpdates: [],
    projects: [project],
    savedViews: [],
    tab: 'activity' as const,
    teams: data.teams,
    users: data.users,
    viewer,
  } as unknown as ComponentProps<typeof ProjectActivity>
}

describe('ProjectActivity', () => {
  it('keeps translated update status controls as one horizontal header row', async () => {
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    try {
      const { container } = render(<I18nProvider><ProjectActivity {...activityProps()} /></I18nProvider>)
      const composer = container.querySelector<HTMLElement>('.project-activity__composer')
      const header = composer?.querySelector(':scope > header')
      const tablist = header?.querySelector('[role="tablist"]')
      const health = header?.querySelector<HTMLButtonElement>('.project-activity__health')

      expect(composer).toHaveAttribute('data-mode', 'update')
      expect(tablist?.children).toHaveLength(2)
      expect(health).toBeTruthy()
      expect(health?.parentElement).toBe(header)
      expect(health).toHaveClass('is-onTrack')
      expect(health?.querySelector('i')).toBeInTheDocument()
      await waitFor(() => expect(health).toHaveTextContent('进展正常'))
      expect(health?.textContent?.trim()).toBe('进展正常')
    } finally {
      if (previousLocale) localStorage.setItem('flow:locale', previousLocale)
      else localStorage.removeItem('flow:locale')
    }
  })
})

