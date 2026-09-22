/**
 * LS-0307 InitiativeListTitle — TitlePrefix helper for list / team chrome.
 */
import type { ReactNode } from 'react'
import { ViewGlyph } from '@/components/views/view-icon-picker'

export function InitiativeListTitle({
  team,
  title = 'Initiatives',
  suffix,
}: {
  team?: { name: string; color?: string; icon?: string } | null
  title?: ReactNode
  suffix?: ReactNode
}) {
  return (
    <h2 className="li-list-title">
      {team && (
        <>
          <ViewGlyph color={team.color} icon={team.icon || 'Team'} />
          <span data-i18n-ignore>{team.name}</span>
          <span aria-hidden="true" className="li-list-title__sep">
            /
          </span>
        </>
      )}
      <span>{title}</span>
      {suffix}
    </h2>
  )
}

export default InitiativeListTitle
