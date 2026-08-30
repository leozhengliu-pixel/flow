import type { ActivityEvent, ProjectSummary, User, WorkspaceMember, WorkflowState } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { NoAssigneeIcon, PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { AssigneeHoverPreview, PropertyShortcutTooltip, StatusHoverPreview } from '@/components/property/issue-property-hover'

export function StatusPicker({ value, states, onChange, hoverHistory }: { value: WorkflowState; states: WorkflowState[]; onChange: (id: string) => void | Promise<void>; hoverHistory?: { activities: ActivityEvent[]; issueCreatedAt: string } }) {
  const options = [...states].sort((left,right)=>(left.position??0)-(right.position??0)).map((state, index) => ({ id: state.id, label: state.name, icon: <StatusIcon state={state}/>, shortcut: String(index + 1) }))
  return <div className="core-property-picker"><PropertyMenu
    label="Status"
    value={value.name}
    selectedId={value.id}
    options={options}
    searchPlaceholder="Change status…"
    searchShortcut="S"
    ariaLabel={`Change status. Current status is ${value.name}`}
    triggerClassName="core-property-trigger"
    trigger={<><StatusIcon state={value}/><span>{value.name}</span></>}
    hoverContent={hoverHistory?<StatusHoverPreview state={value} activities={hoverHistory.activities} issueCreatedAt={hoverHistory.issueCreatedAt}/>:undefined}
    hoverClassName="property-rich-hover"
    onChange={onChange}
  /></div>
}

export function PriorityPicker({ value, onChange }: { value: number; onChange: (value: number) => void | Promise<void> }) {
  const labels = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
  const options = labels.map((label, priority) => ({ id: String(priority), label, icon: <PriorityIcon priority={priority}/>, shortcut: String(priority) }))
  return <div className="core-property-picker"><PropertyMenu
    label="Priority"
    value={value ? labels[value] : 'Set priority'}
    selectedId={String(value)}
    options={options}
    searchPlaceholder="Change priority…"
    ariaLabel={`Change priority. ${value ? `${labels[value]} is selected` : 'No priority is selected'}`}
    triggerClassName={`core-property-trigger${value === 0 ? ' muted' : ''}`}
    trigger={<><PriorityIcon priority={value}/><span>{value ? labels[value] : 'Set priority'}</span></>}
    hoverContent={<PropertyShortcutTooltip label="Change priority" shortcut="P"/>}
    onChange={id => onChange(Number(id))}
  /></div>
}

export function AssigneePicker({ value, users, onChange, hoverContext }: { value?: User; users: User[]; onChange: (id: string) => void | Promise<void>; hoverContext?: { member?: WorkspaceMember; workspaceName: string; project?: ProjectSummary } }) {
  const options = [{ id: '', label: 'No assignee', icon: <NoAssigneeIcon size={15}/> }, ...users.map(user => ({ id: user.id, label: user.displayName, keywords: user.email, icon: <Avatar name={user.displayName}/> }))]
  return <div className="core-property-picker"><PropertyMenu
    label="Assignee"
    value={value?.displayName ?? 'Assign to…'}
    selectedId={value?.id ?? ''}
    options={options}
    searchPlaceholder="Change assignee…"
    ariaLabel={`Change assignee. ${value ? `${value.displayName} is assigned` : 'Currently no one is assigned.'}`}
    triggerClassName={`core-property-trigger${value ? '' : ' muted'}`}
    trigger={<>{value ? <Avatar name={value.displayName}/> : <NoAssigneeIcon size={15}/>}<span>{value?.displayName ?? 'Assign to…'}</span></>}
    hoverContent={value&&hoverContext?<AssigneeHoverPreview user={value} {...hoverContext}/>:undefined}
    hoverClassName="property-rich-hover assignee-hover-surface"
    onChange={onChange}
  /></div>
}
