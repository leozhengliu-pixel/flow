import { Send } from 'lucide-react'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import type { User } from '@/types/flow'
import { usePeopleDirectory } from '@/components/property/people-context'

export function ProjectLeadPicker({ emptyTriggerLabel = 'Lead', value, users, teamIds = [], onChange, onInvite, triggerClassName = 'mini-property-trigger', side = 'bottom', alignOffset = 0, surfaceClassName = 'project-details-sidebar__property-menu is-lead', open, onOpenChange, tooltip, tooltipShortcut }: {
  emptyTriggerLabel?: string; value?: User; users: User[]; teamIds?: string[]; onChange: (id: string) => void | Promise<unknown>; onInvite?: () => void; triggerClassName?: string; side?: 'bottom'|'left'; alignOffset?: number; surfaceClassName?: string
  open?: boolean; onOpenChange?: (open: boolean) => void; tooltip?: string; tooltipShortcut?: string
}) {
  const directory=usePeopleDirectory()
  const scoped=teamIds.length>0 && directory.teamIds.size>0
  const inTeam=(id:string)=>(directory.teamIds.get(id)??[]).some(team=>teamIds.includes(team))
  return <PersonPicker ariaLabel={`Change Lead. Current value is ${value?.displayName ?? 'none'}`} label="Lead" emptyTriggerLabel={emptyTriggerLabel} emptyOptionLabel="No lead" emptyOptionShortcut="0"
    people={users.map(user => ({ ...user, label: user.displayName || user.name, disabled: user.active === false && user.id !== value?.id, end: user.active === false ? 'Inactive' : undefined, searchOnly: scoped && !inTeam(user.id) && user.id!==value?.id, groupLabel: user.id===value?.id || !scoped ? undefined : inTeam(user.id)?'Users from the project team':'Other users' }))}
    hideSearch searchPlaceholder="Set lead…" searchShortcut="P, then A" selectedId={value?.id} surfaceClassName={surfaceClassName} triggerClassName={triggerClassName} side={side} alignOffset={alignOffset} open={open} onOpenChange={onOpenChange} tooltip={tooltip} tooltipShortcut={tooltipShortcut}
    onChange={id => { if (id === '__invite-project-member__') onInvite?.(); else void onChange(id) }}
    extraOptions={onInvite ? [{id:'__invite-project-member__',label:'Invite and add…',icon:<Send size={14}/>,groupLabel:'New user'}] : []}/>
}
