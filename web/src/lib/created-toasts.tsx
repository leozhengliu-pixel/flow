import { ProjectGlyph } from '@/components/views/project-glyph'
import { InitiativeStatusIcon } from '@/components/initiatives/initiative-shared'
import { StatusIcon } from '@/components/issue/issue-icons'
import { toast as sonnerToast } from 'sonner'
import { IssueCreatedToast } from '@/components/ui/issue-created-toast'
import { toast } from '@/lib/toast'
import type { BootstrapData, Initiative, Issue, IssueUpdateInput, Project } from '@/types/flow'

type Translate = (source: string) => string
const identity: Translate = source => source

/** How long the New issue dialog's "Issue created" stays up in the reference app (an issue made from a selection: 6s). */
export const DIALOG_ISSUE_CREATED_TOAST_DURATION = 10000

/**
 * The New issue dialog's "Issue created", as the reference app shows it: a success check, the issue as a chip reading
 * "FLO-41 ‒ Title" after its status, and a "View issue" link; 10s. An issue made in place from a selection gets
 * The reference app's other "Issue created" card instead (toastIssueCreatedInPlace).
 */
export function toastIssueCreated(issue: Pick<Issue, 'identifier' | 'title' | 'state'>, href: string, t: Translate = identity) {
  return toast.success(t('Issue created'), {
    entity: { icon: <StatusIcon size={16} state={issue.state}/>, title: `${issue.identifier} ‒ ${issue.title}`, href },
    action: { label: t('View issue'), href },
    duration: DIALOG_ISSUE_CREATED_TOAST_DURATION,
  })
}

/** How long the reference app's in-place "Issue created" (an issue made from a selection) stays up; hover pauses it. */
export const ISSUE_CREATED_IN_PLACE_TOAST_DURATION = 6000

/** "Issue created" for an issue made in place from a selection: the reference app's card with "Ask Flow" and quick actions. */
export function toastIssueCreatedInPlace(issue: Issue, href: string, data: BootstrapData, actions: { onUpdate?: (input: IssueUpdateInput) => Promise<unknown>; latestData?: () => BootstrapData | undefined } = {}) {
  return sonnerToast.custom(id => <IssueCreatedToast data={data} href={href} id={id} issue={issue} latestData={actions.latestData} onUpdate={actions.onUpdate}/>, {
    id: `flow-issue-created:${issue.id}`,
    duration: ISSUE_CREATED_IN_PLACE_TOAST_DURATION,
  })
}

export function toastProjectCreated(project: Pick<Project, 'name' | 'icon' | 'color'>, href: string, t: Translate = identity) {
  return toast.success(t('Project created'), {
    entity: { icon: <ProjectGlyph project={project}/>, title: project.name, href },
    action: { label: t('View project'), href },
  })
}

export function toastInitiativeCreated(initiative: Pick<Initiative, 'name' | 'status'>, href: string, t: Translate = identity) {
  return toast.success(t('Initiative created'), {
    entity: { icon: <InitiativeStatusIcon status={initiative.status}/>, title: initiative.name, href },
    action: { label: t('View initiative'), href },
  })
}
