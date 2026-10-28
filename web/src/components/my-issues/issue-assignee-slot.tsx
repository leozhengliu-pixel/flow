import { NoAssigneeIcon } from '@/components/issue/issue-icons'
import { AgentHoverCard } from '@/components/agent/agent-hover-card'
import { AGENT_HOVER_CLASS, agentHandle } from '@/components/agent/agent-hover-class'
import { IssueAssigneeMenu } from '@/components/issue/core-property-pickers'
import { avatarColor } from '@/components/issue/avatar-color'
import { usePeopleDirectory } from '@/components/property/people-context'
import { UserAvatar } from '@/components/ui/user-avatar'
import { directoryPerson, personIdentifier } from '@/lib/people'
import { CLEAR_ASSIGNEE_AND_AGENT_ID, NO_AGENT_ID } from '@/lib/agent-members'
import type { User } from '@/types/flow'
import type { MyIssuesContextOption } from './my-issues-list'
import styles from './my-issues-list.module.css'
import './issue-assignee-slot.css'

type Person = { id: string; name: string; avatarUrl?: string; color?: string }

/**
 * The assignee cell of a list row or board card, as the reference app draws it: the delegated agent's 18px avatar
 * sits behind the human assignee's (4.5px overlap, a ring where they meet); an agent with no human shows
 * the empty-assignee glyph in the human's place. One hover card describes both ("Name (handle)" with the agent on a
 * connector beneath, or the agent card alone) and the picker is the issue's shared assignee menu
 * (`IssueAssigneeMenu`): the agent hangs under the assignee with an "Agent" pill and opens its agent submenu.
 */
export function IssueAssigneeSlot({ issue, options, onChange, triggerClassName, avatarClassName, emptyClassName, label, hoverPlacement = 'below' }: {
  issue: { assignee?: Person; delegate?: Person; agentSessionState?: string }
  options: MyIssuesContextOption[]
  onChange: (id: string) => void | Promise<void>
  triggerClassName?: string
  avatarClassName: string
  emptyClassName?: string
  label?: string
  hoverPlacement?: 'left' | 'below'
}) {
  const { users } = usePeopleDirectory()
  const { assignee, delegate } = issue
  const assigneeUser = assignee ? toUser(assignee, users) : undefined
  const delegateUser = delegate ? { ...toUser(delegate, users), app: true } : undefined
  const people = options.filter(option => option.id).map(option => {
    const known = directoryPerson(users, option.id)
    return { ...(known ?? { id: option.id, name: option.handle ?? option.label, email: '', active: true }), displayName: option.label, avatarUrl: option.avatarUrl ?? known?.avatarUrl, app: Boolean(option.agent) } as User
  })
  const ariaLabel = delegate && !assignee ? `Change assignee. ${delegate.name} is delegated` : assignee ? (delegate ? `Change assignee. ${assignee.name} is assigned` : `Assign to. Current assignee is ${assignee.name}`) : (label ?? 'Assign issue')
  const hover = assigneeUser && delegateUser ? <AssigneeDelegateCard assignee={assigneeUser} delegate={delegateUser}/> : delegateUser ? <AgentHoverCard agent={delegateUser}/> : undefined
  const hoverClassName = assignee && delegate ? 'assignee-slot-hover-surface' : delegate ? AGENT_HOVER_CLASS : undefined
  const cluster = <span className="issue-assignee-cluster" data-delegated={delegate ? '' : undefined}>
    {delegate && <UserAvatar avatarUrl={delegate.avatarUrl} className={`${avatarClassName} issue-assignee-cluster__agent`} color={delegate.color ?? avatarColor(delegate.id)} name={delegate.name}/>}
    {assignee
      ? <UserAvatar avatarUrl={assignee.avatarUrl} className={`${avatarClassName} issue-assignee-cluster__member`} color={assignee.color ?? 'var(--avatar-fallback)'} name={assignee.name}/>
      : <span className={`${emptyClassName ?? ''} issue-assignee-cluster__empty`}><NoAssigneeIcon size={18}/></span>}
  </span>
  return <IssueAssigneeMenu
    ariaLabel={ariaLabel}
    assignee={assigneeUser}
    delegate={delegateUser}
    delegateSessionActive={issue.agentSessionState === 'pending' || issue.agentSessionState === 'active'}
    hoverClassName={hoverClassName}
    hoverContent={hover}
    hoverPlacement={hoverPlacement}
    onAssign={onChange}
    onDelegate={id => onChange(id || NO_AGENT_ID)}
    onUnassignAll={() => onChange(CLEAR_ASSIGNEE_AND_AGENT_ID)}
    people={people}
    trigger={cluster}
    triggerClassName={triggerClassName ?? styles.propertyTrigger}
    value={assignee?.name}
  />
}

function toUser(person: Person, users: ReadonlyMap<string, User>): User {
  const known = directoryPerson(users, person.id)
  return known ?? ({ id: person.id, name: person.name, displayName: person.name, avatarUrl: person.avatarUrl, email: '', active: true } as User)
}

/** "Name (handle)" over the agent on a connector: the reference hover card of an assignee with a delegate. */
function AssigneeDelegateCard({ assignee, delegate }: { assignee: User; delegate: User }) {
  const handle = assignee.username || personIdentifier(assignee)
  const agentName = delegate.displayName || delegate.name
  return <div className="assignee-slot-card">
    <div className="assignee-slot-card__person">
      <UserAvatar avatarUrl={assignee.avatarUrl} className="assignee-slot-card__avatar" color={avatarColor(assignee.id)} name={assignee.displayName || assignee.name}/>
      <span data-i18n-ignore>{assignee.displayName || assignee.name}</span>{handle && <em data-i18n-ignore>({handle})</em>}
    </div>
    <div className="assignee-slot-card__agent">
      <i aria-hidden="true"/>
      <UserAvatar avatarUrl={delegate.avatarUrl} className="assignee-slot-card__avatar" color={avatarColor(delegate.id)} name={agentName}/>
      <span data-i18n-ignore>{agentName}</span><em data-i18n-ignore>({agentHandle(delegate)})</em>
      <span className="assignee-slot-agent-pill" data-i18n-ignore>Agent</span>
    </div>
  </div>
}
