import { ReleasesIcon, ReleaseStatusIcon } from '@/components/releases/release-icons'
import { useI18n } from '@/i18n/i18n'
import type { Release } from '@/types/flow'

/** What an issue row or card needs to show one release. */
export interface IssueReleaseEntry {
  id: string
  name: string
  pipelineName?: string
  status: Release['status']
  color?: string
}

/**
 * The Release display property on issue rows and board cards (Linear): one
 * release shows its stage icon and "Pipeline · Release"; several collapse to
 * "N releases" behind the plain release glyph.
 */
export function IssueReleaseChip({ className, iconSize = 13, releases }: { className?: string; iconSize?: number; releases: IssueReleaseEntry[] }) {
  const { t } = useI18n()
  if (!releases.length) return null
  const single = releases.length === 1 ? releases[0] : undefined
  const label = single
    ? single.pipelineName ? `${single.pipelineName} · ${single.name}` : single.name
    : t('{count} releases').replace('{count}', String(releases.length))
  return <span aria-label={single ? `${t('Release')} ${label}` : label} className={className} data-issue-release-chip="">
    {single ? <ReleaseStatusIcon color={single.color} size={iconSize} status={single.status}/> : <ReleasesIcon size={iconSize}/>}
    <span data-i18n-ignore={single ? true : undefined}>{label}</span>
  </span>
}
