import type { ReactElement } from 'react'
import { FlowTooltip, TooltipShortcut } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import './sidebar-issues-tooltip.css'

export function SidebarIssuesTooltip({ children }: { children: ReactElement }) {
  const { t } = useI18n()
  return <FlowTooltip side="right" disableHoverableContent contentClassName="sidebar-issues-tooltip" label={<span className="sidebar-issues-shortcuts">
    {([['All issues', 'G then E'], ['Active', 'G then A'], ['Backlog', 'G then B']] as const).map(([label, shortcut]) => <span className="sidebar-issues-shortcut" key={label}><span>{t(label)}</span><TooltipShortcut value={shortcut}/></span>)}
  </span>}>{children}</FlowTooltip>
}
