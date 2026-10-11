import { ProjectGlyph } from '@/components/views/project-glyph'
import { InitiativeStatusIcon } from '@/components/initiatives/initiative-shared'
import { StatusIcon } from '@/components/issue/issue-icons'
import { toast } from '@/lib/toast'
import type { Initiative, Issue, Project } from '@/types/flow'

type Translate = (source: string) => string
const identity: Translate = source => source

/** "Issue created" confirmation: the issue as a chip plus a "View issue" link. */
export function toastIssueCreated(issue: Pick<Issue, 'identifier' | 'title' | 'state'>, href: string, t: Translate = identity) {
  return toast.success(t('Issue created'), {
    entity: { icon: <StatusIcon size={16} state={issue.state}/>, identifier: issue.identifier, title: issue.title, href },
    action: { label: t('View issue'), href },
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
