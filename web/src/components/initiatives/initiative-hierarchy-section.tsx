import { useMemo, useState } from 'react'
import { Plus, Unlink } from 'lucide-react'
import { toast } from 'sonner'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { PropertyMenu } from '@/components/property/property-menu'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import type { Initiative, InitiativeMutationInput, IssueLabel, Team, User } from '@/types/flow'
import { initiativeGraph } from './initiative-hierarchy'
import { InitiativeCreateRow } from './initiatives-page'
import './initiative-hierarchy.css'

export function InitiativeHierarchySection({ initiative, initiatives, teams, users, labels, viewer, onOpen, onUpdate, onCreate, onCreateLabel }: {
  initiative: Initiative; initiatives: Initiative[]; teams: Team[]; users: User[]; labels: IssueLabel[]; viewer: User
  onOpen: (initiative: Initiative) => void
  onUpdate: (id: string, input: InitiativeMutationInput) => Promise<Initiative>
  onCreate: (input: InitiativeMutationInput & { name: string }) => Promise<Initiative>
  onCreateLabel: (name: string) => Promise<IssueLabel>
}) {
  const { t } = useI18n(), [creating, setCreating] = useState(false), [busy, setBusy] = useState(false)
  const graph = useMemo(() => initiativeGraph(initiatives), [initiatives])
  const parents = (initiative.parentInitiativeIds ?? []).map(id => graph.byId.get(id)).filter((item): item is Initiative => Boolean(item))
  const children = (graph.children.get(initiative.id) ?? []).map(id => graph.byId.get(id)!)
  const option = (item: Initiative) => ({ id: item.id, label: item.name, disabled: busy, i18nIgnore: true, icon: <ViewGlyph icon={item.icon || 'Initiative'} color={item.color}/> })
  const save = async (id: string, input: InitiativeMutationInput) => {
    if (busy) return
    setBusy(true)
    try { await onUpdate(id, input) } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not update initiative')) } finally { setBusy(false) }
  }
  return <section className="li-hierarchy-section">
    <div className="li-parent-initiatives"><span>{t('Parent initiatives')}</span>{parents.map(parent => <button key={parent.id} type="button" onClick={() => onOpen(parent)}><ViewGlyph icon={parent.icon || 'Initiative'} color={parent.color}/><span data-i18n-ignore>{parent.name}</span></button>)}
      <PropertyMenu label="Parent initiatives" ariaLabel={t('Change parent initiatives')} multiple closeOnSelect={false} selectedIds={parents.map(item => item.id)} options={initiatives.filter(item => parents.some(parent => parent.id === item.id) || graph.canParent(initiative.id, item.id)).map(option)} onChange={id => save(initiative.id, { parentInitiativeIds: (initiative.parentInitiativeIds ?? []).includes(id) ? (initiative.parentInitiativeIds ?? []).filter(parent => parent !== id) : [...(initiative.parentInitiativeIds ?? []), id] })} trigger={<Plus size={14}/>} triggerClassName="li-hierarchy-add"/>
    </div>
    <header><h3>{t('Sub-initiatives')}</h3><span>{children.length}</span><PropertyMenu label="Add existing initiative" ariaLabel={t('Add existing sub-initiative')} options={initiatives.filter(item => !children.some(child => child.id === item.id) && graph.canParent(item.id, initiative.id)).map(option)} onChange={id => save(id, { parentInitiativeIds: [...(graph.byId.get(id)?.parentInitiativeIds ?? []), initiative.id] })} trigger={<><Plus size={14}/>{t('Add existing')}</>} triggerClassName="li-hierarchy-action"/><button type="button" className="li-hierarchy-action" disabled={busy} onClick={() => setCreating(true)}><Plus size={14}/>{t('New sub-initiative')}</button></header>
    {children.length ? children.map(child => <div className="li-child-initiative" key={child.id}><button type="button" className="li-child-name" onClick={() => onOpen(child)}><ViewGlyph icon={child.icon || 'Initiative'} color={child.color}/><strong data-i18n-ignore>{child.name}</strong></button><span>{t(child.status[0].toUpperCase() + child.status.slice(1))}</span><span data-i18n-ignore>{child.owner?.displayName}</span><span>{graph.projectIds(child.id).size} {t('Projects')}</span><button aria-label={`${t('Remove sub-initiative')}: ${child.name}`} title={t('Remove from this initiative')} type="button" disabled={busy} onClick={() => save(child.id, { parentInitiativeIds: (child.parentInitiativeIds ?? []).filter(id => id !== initiative.id) })}><Unlink size={14}/></button></div>) : <p className="li-hierarchy-empty">{t('No sub-initiatives')}</p>}
    <Dialog open={creating} onOpenChange={setCreating}><DialogContent className="li-subinitiative-dialog"><DialogTitle>{t('New sub-initiative')}</DialogTitle><InitiativeCreateRow initialLeadTeamId={initiative.leadTeamId} labels={labels} teams={teams} users={users} viewer={viewer} view="planned" onCancel={() => setCreating(false)} onCreateLabel={onCreateLabel} onCreate={async input => { try { await onCreate({ ...input, parentInitiativeIds: [initiative.id] }); setCreating(false) } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not create initiative')) } }}/></DialogContent></Dialog>
  </section>
}
