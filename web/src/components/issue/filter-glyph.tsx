import type { ReactNode } from 'react'
import { CycleIcon, NoAssigneeIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { ViewGlyph } from '@/components/views/view-icon-picker'

const SPRITE: Record<string, string> = {
  status: 'IssueStatusBacklog',
  labels: 'Label',
  'suggested label': 'Label',
  dates: 'Calendar',
  'added to cycle': 'Calendar',
  project: 'Project',
  initiative: 'Initiative',
  initiatives: 'Initiative',
  subscribers: 'Subscribe',
  links: 'Link',
  'lead team': 'Team',
  'contributing teams': 'Team',
  teams: 'Team',
}
const PEOPLE = new Set(['assignee', 'creator', 'lead', 'members', 'owner', 'from'])

/** Filter menu icon for a field label, using Flow's glyphs where one exists. */
export function FilterGlyph({ label, fallback }: { label: string; fallback: ReactNode }) {
  const key = label.trim().toLowerCase()
  const sprite = SPRITE[key]
  if (sprite) return <ViewGlyph icon={sprite} color="currentColor" style={{ width: 14, height: 14 }}/>
  if (key === 'priority') return <PriorityIcon priority={2} size={14} aria-hidden aria-label={undefined} role={undefined}/>
  if (PEOPLE.has(key)) return <NoAssigneeIcon size={14} aria-hidden/>
  if (key === 'cycle') return <CycleIcon noCycle size={14} aria-hidden/>
  if (key === 'relations') return <IssueActionGlyph label="Mark as" fallback={fallback}/>
  return <>{fallback}</>
}
