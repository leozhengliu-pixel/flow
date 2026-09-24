/**
 * LS-0608 TimelineActions — preference + command pack for project timeline.
 * Linear: Toggle timeline project list (Shift+{) → timelineShowProjectsAside;
 * Select project (x); activate+select helpers.
 */
import { ClientStorage } from '@/lib/client-storage'
import type { RegisteredAction } from '@/lib/action-registry'

export const TIMELINE_SHOW_PROJECTS_ASIDE_KEY = 'flow:projects:timelineShowProjectsAside'

export type TimelineActionsContext = {
  showProjectsAside: boolean
  setShowProjectsAside: (next: boolean) => void
  selectedProjectId?: string
  selectProject: (projectId: string | undefined) => void
  /** Optional activate callback when opening a project from the aside. */
  activateProject?: (projectId: string) => void
}

export function readTimelineShowProjectsAside(defaultValue = true): boolean {
  const stored = ClientStorage.get<boolean>(TIMELINE_SHOW_PROJECTS_ASIDE_KEY, {
    storageMechanism: 'local',
    logError: false,
  })
  return typeof stored === 'boolean' ? stored : defaultValue
}

export function writeTimelineShowProjectsAside(value: boolean) {
  ClientStorage.set(TIMELINE_SHOW_PROJECTS_ASIDE_KEY, value, 'local')
}

export function toggleTimelineShowProjectsAside(current: boolean): boolean {
  const next = !current
  writeTimelineShowProjectsAside(next)
  return next
}

/** Build ActionRegistry entries for command palette / keyboard. */
export function buildTimelineActions(ctx: TimelineActionsContext): RegisteredAction[] {
  return [
    {
      id: 'timeline.toggle-projects-aside',
      label: 'Toggle timeline project list',
      keywords: 'show left sidebar aside toggle timeline',
      shortcut: ['Shift', '{'],
      group: 'Projects',
      run: () => {
        ctx.setShowProjectsAside(toggleTimelineShowProjectsAside(ctx.showProjectsAside))
      },
    },
    {
      id: 'timeline.select-project',
      label: 'Select project',
      keywords: 'select project timeline',
      shortcut: ['X'],
      group: 'Projects',
      enabled: context => Boolean((context.payload as { projectId?: string } | undefined)?.projectId),
      run: context => {
        const projectId = (context.payload as { projectId?: string } | undefined)?.projectId
        if (!projectId) return
        if (ctx.selectedProjectId === projectId) ctx.selectProject(undefined)
        else ctx.selectProject(projectId)
      },
    },
  ]
}

/** Activate + select a timeline row (Linear `n(e, item)` helper). */
export function activateAndSelectTimelineProject(
  ctx: TimelineActionsContext,
  projectId: string,
) {
  ctx.activateProject?.(projectId)
  ctx.selectProject(projectId)
}

export function matchesTimelineAsideShortcut(event: KeyboardEvent): boolean {
  return event.shiftKey && (event.key === '{' || event.code === 'BracketLeft')
}

export function matchesTimelineSelectShortcut(event: KeyboardEvent): boolean {
  return !event.metaKey && !event.ctrlKey && !event.altKey && event.key.toLowerCase() === 'x'
}
