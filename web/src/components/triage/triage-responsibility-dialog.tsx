import { useMemo, useState } from 'react'
import { toast } from 'sonner'
import { updateStructuredTeamSettings } from '@/lib/api'
import { PropertyMenu } from '@/components/property/property-menu'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SelectControl } from '@/components/ui/select-control'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Team, TeamSettings } from '@/types/flow'

const ACTIONS = [
  { value: 'none', label: 'No action' },
  { value: 'notify', label: 'Notify' },
  { value: 'assign', label: 'Assign' },
] as const
/** Flow's earlier rule-based actions stay selectable for teams that already use them. */
const LEGACY_ACTIONS: Record<string, string> = { creator: 'Assign to issue creator', teamOwner: 'Assign to team owner', responsibility: 'Use responsibility' }

/** Member picker for the Notify (several) / Assign (one) triage responsibility. */
export function TriageResponsibilityMembers({ data, teamId, multiple, value, disabled = false, onChange }: {
  data: BootstrapData
  teamId: string
  multiple: boolean
  value: string[]
  disabled?: boolean
  onChange: (ids: string[]) => void
}) {
  const { t } = useI18n()
  const members = useMemo(() => {
    const ids = new Set(data.teamMembers.filter(member => member.teamId === teamId).map(member => member.userId))
    const scoped = data.users.filter(user => ids.has(user.id))
    return (scoped.length ? scoped : data.users).filter(user => user.active !== false && !user.app)
  }, [data.teamMembers, data.users, teamId])
  const selected = members.filter(user => value.includes(user.id))
  const label = t(multiple ? 'Members to notify' : 'Assignee')
  const toggle = (id: string) => onChange(!multiple ? [id] : value.includes(id) ? value.filter(item => item !== id) : [...value, id])
  return <PropertyMenu
    label={label}
    ariaLabel={label}
    multiple={multiple}
    closeOnSelect={!multiple}
    selectedId={value[0]}
    selectedIds={value}
    options={members.map(user => ({ id: user.id, label: user.displayName, i18nIgnore: true, icon: <UserAvatar className="user-avatar" name={user.displayName} avatarUrl={user.avatarUrl}/> }))}
    searchPlaceholder={t('Search people…')}
    triggerClassName="select-control triage-responsibility-members"
    trigger={<span data-i18n-ignore={selected.length ? true : undefined} aria-disabled={disabled || undefined}>{selected.length ? selected.map(user => user.displayName).join(', ') : t('Select members…')}</span>}
    onChange={toggle}
  />
}

/** Linear's "Configure triage responsibility" dialog: No action / Notify members / Assign a member. */
export function TriageResponsibilityDialog({ data, team, open, onOpenChange, onSaved }: {
  data: BootstrapData
  team: Team
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved?: (settings: TeamSettings) => void | Promise<void>
}) {
  const { t } = useI18n()
  const persisted = data.teamSettings?.[team.id]
  const [action, setAction] = useState(() => normalizedAction(persisted?.triageAction))
  const [userIds, setUserIds] = useState<string[]>(() => persisted?.triageActionUserIds ?? [])
  const [saving, setSaving] = useState(false)
  const withMembers = action === 'notify' || action === 'assign'
  const save = async (nextAction: string, nextUsers: string[]) => {
    setSaving(true)
    try {
      const saved = await updateStructuredTeamSettings(team.id, { triageAction: nextAction, triageActionUserIds: nextAction === 'notify' || nextAction === 'assign' ? nextUsers : [] })
      await onSaved?.(saved)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not update triage responsibility'))
    } finally { setSaving(false) }
  }
  const changeAction = (next: string) => {
    const users = next === 'assign' ? userIds.slice(0, 1) : userIds
    setAction(next)
    setUserIds(users)
    void save(next, users)
  }
  const changeUsers = (users: string[]) => {
    setUserIds(users)
    void save(action, users)
  }
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent aria-describedby={undefined} className="triage-responsibility-dialog" closeLabel={t('Close dialog')}>
      <DialogTitle>{t('Triage responsibility settings')}</DialogTitle>
      <p className="triage-responsibility-dialog__subtitle">{t('Define how incoming issues and requests are handled in triage')}</p>
      <div className="triage-responsibility-dialog__row">
        <div>
          <strong>{t('Action')}</strong>
          <span>{t('When a new issue is added to triage, take the following action')}</span>
        </div>
        <SelectControl align="end" className="triage-responsibility-dialog__select" label={t('Action')} value={action} disabled={saving} options={[...ACTIONS.map(item => ({ value: item.value, label: t(item.label) })), ...(LEGACY_ACTIONS[action] ? [{ value: action, label: t(LEGACY_ACTIONS[action]) }] : [])]} onChange={changeAction}/>
      </div>
      {withMembers ? <div className="triage-responsibility-dialog__row">
        <div>
          <strong>{t(action === 'assign' ? 'Assignee' : 'Members to notify')}</strong>
          <span>{t(action === 'assign' ? 'New triage issues are assigned to this member' : 'These members get an inbox notification for each new triage issue')}</span>
        </div>
        <TriageResponsibilityMembers data={data} teamId={team.id} multiple={action === 'notify'} value={userIds} disabled={saving} onChange={changeUsers}/>
      </div> : null}
    </DialogContent>
  </Dialog>
}

function normalizedAction(value?: string) {
  return value && (value === 'notify' || value === 'assign' || LEGACY_ACTIONS[value]) ? value : 'none'
}
