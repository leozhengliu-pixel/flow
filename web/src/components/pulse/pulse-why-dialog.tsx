import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, X } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, PulseItem } from '@/types/flow'
import { primaryPulseReason, pulseReasonCopy } from './pulse-why-copy'

export function PulseWhyDialog({ data, item, open, onOpenChange, onToggleSubscription, onToggleTeam }: {
  data: Pick<BootstrapData, 'projects' | 'initiatives' | 'teams'>
  item: PulseItem
  open: boolean
  onOpenChange: (open: boolean) => void
  onToggleSubscription: () => void
  onToggleTeam: (teamId: string, subscribed: boolean) => void
}) {
  const { t } = useI18n()
  const reason = primaryPulseReason(item.reasons)
  const copy = reason ? pulseReasonCopy(item, reason, data, t) : undefined
  const teamIds = reason?.type === 'teamProjectUpdates' ? reason.sourceIds ?? [] : []
  const teams = teamIds.map(id => data.teams.find(team => team.id === id)).filter((team): team is BootstrapData['teams'][number] => Boolean(team)).sort((left, right) => left.name.localeCompare(right.name))
  const type = item.kind === 'project' ? t('project') : t('initiative')
  const subscribeLabel = (item.subscribed ? t('Unsubscribe from this {type}') : t('Subscribe to this {type}')).replace('{type}', type)
  return <Dialog.Root onOpenChange={onOpenChange} open={open}>
    <Dialog.Portal>
      <Dialog.Overlay className="pulse-dialog-overlay" data-flow-motion="backdrop"/>
      <Dialog.Content aria-describedby={undefined} className="pulse-why-dialog" data-flow-motion="dialog">
        <header><Dialog.Title>{t('Why am I seeing this?')}</Dialog.Title><Dialog.Close asChild><button aria-label={t('Close')} className="pulse-icon-button" type="button"><X size={14}/></button></Dialog.Close></header>
        <div className="pulse-why-body">
          {copy && <p>{copy.text}</p>}
          {copy?.footer && <p>{t(copy.footer)}</p>}
          <div className="pulse-why-actions" data-team={teams.length > 0 || undefined}>
            {teams.length === 1 && <button className="pulse-link-button" onClick={() => { onToggleTeam(teams[0].id, false); onOpenChange(false) }} type="button">{t('Unsubscribe from team')}</button>}
            {teams.length > 1 && <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="pulse-link-button" type="button">{t('Manage subscriptions')}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="pulse-menu" data-flow-motion="floating" sideOffset={4}>
              <DropdownMenu.Label className="pulse-menu-label">{t('Team project updates')}</DropdownMenu.Label>
              {teams.map(team => <DropdownMenu.Item key={team.id} onSelect={event => { event.preventDefault(); onToggleTeam(team.id, false) }}><span data-i18n-ignore>{team.name}</span><Check className="pulse-menu-end" size={13}/></DropdownMenu.Item>)}
            </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}
            <button className="pulse-secondary-button" onClick={() => { onToggleSubscription(); onOpenChange(false) }} type="button">{subscribeLabel}</button>
          </div>
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
