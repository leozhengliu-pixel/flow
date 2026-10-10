import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { translateToChinese } from '@/i18n/translate'
import type { BootstrapData, PulseItem, PulseReason } from '@/types/flow'
import { primaryPulseReason, pulseReasonCopy } from './pulse-why-copy'
import { PulseWhyDialog } from './pulse-why-dialog'

const data = {
  projects: [{ id: 'p1', name: 'Mobile app' }],
  initiatives: [{ id: 'i1', name: 'Q4 launch' }, { id: 'i2', name: 'Platform' }],
  teams: [{ id: 't1', name: 'Engineering' }, { id: 't2', name: 'Design' }],
} as unknown as Pick<BootstrapData, 'projects' | 'initiatives' | 'teams'>
const t = (value: string) => value
const projectItem = { kind: 'project', source: { id: 'p1', name: 'Mobile app', url: '/w/project/p1' } } as PulseItem
const initiativeItem = { kind: 'initiative', source: { id: 'i1', name: 'Q4 launch', url: '/w/initiative/i1' } } as PulseItem
const copy = (item: PulseItem, reason: PulseReason) => pulseReasonCopy(item, reason, data, t)

describe('Why am I seeing this?', () => {
  it('uses Linear copy for every reason', () => {
    expect(copy(projectItem, { type: 'mentioned' }).plain).toBe('You’re mentioned in this update.')
    expect(copy(projectItem, { type: 'author' }).plain).toBe('You’re the author of this update.')
    expect(copy(projectItem, { type: 'projectMember' }).plain).toBe('As a member of Mobile app, you’re automatically subscribed to updates on this project.')
    expect(copy(projectItem, { type: 'subscribed' }).plain).toBe('You’re subscribed to updates on Mobile app.')
    expect(copy(projectItem, { type: 'initiativeOwner', sourceIds: ['i1', 'i2'] }).plain).toBe('As an owner of Q4 launch and Platform, you’re automatically subscribed to updates on this project.')
    expect(copy(projectItem, { type: 'initiativeOwner' }).plain).toBe('As an owner of an initiative this project belongs to, you’re automatically subscribed to updates on this project.')
    expect(copy(initiativeItem, { type: 'initiativeOwner' }).plain).toBe('As an owner of Q4 launch, you’re automatically subscribed to updates on this initiative.')
    expect(copy(initiativeItem, { type: 'initiativeProjectMember' }).plain).toBe('As a member of a project belonging to Q4 launch, you’re automatically subscribed to updates on this initiative.')
    expect(copy(projectItem, { type: 'teamProjectUpdates', sourceIds: ['t1', 't2'] }).plain).toBe('You’re subscribed to project updates for the Engineering and Design teams.')
    expect(copy(projectItem, { type: 'teamProjectUpdates', names: ['Engineering'] }).plain).toBe('You’re subscribed to project updates for the Engineering team.')
  })

  it('picks the footer by reason and prefers mentions and authorship', () => {
    expect(copy(projectItem, { type: 'author' }).footer).toBeUndefined()
    expect(copy(projectItem, { type: 'subscribed' }).footer).toBe('Modify your Pulse subscriptions from an initiative or project page using the subscription menu in the top-right corner.')
    expect(copy(projectItem, { type: 'teamProjectUpdates', sourceIds: ['t1'] }).footer).toBe('Modify your Pulse subscriptions from a team’s menu in the sidebar.')
    expect(primaryPulseReason([{ type: 'projectMember' }, { type: 'author' }, { type: 'mentioned' }])?.type).toBe('mentioned')
    expect(primaryPulseReason([{ type: 'projectMember' }, { type: 'author' }])?.type).toBe('author')
  })

  it('has Chinese copy for the reason templates', () => {
    const zh = pulseReasonCopy(projectItem, { type: 'projectMember' }, data, translateToChinese)
    expect(zh.plain).not.toMatch(/automatically subscribed/)
    expect(zh.plain).toContain('Mobile app')
  })

  it('renders the 543px dialog with the subscription toggle and team unsubscribe', () => {
    const onToggleSubscription = vi.fn()
    const onToggleTeam = vi.fn()
    const onOpenChange = vi.fn()
    const item = { ...projectItem, subscribed: true, reasons: [{ type: 'teamProjectUpdates', sourceIds: ['t1'] }] } as PulseItem
    render(<I18nProvider><PulseWhyDialog data={data} item={item} onOpenChange={onOpenChange} onToggleSubscription={onToggleSubscription} onToggleTeam={onToggleTeam} open/></I18nProvider>)
    expect(screen.getByRole('dialog', { name: 'Why am I seeing this?' })).toHaveClass('pulse-why-dialog')
    expect(screen.getByText('the Engineering team')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Unsubscribe from team' }))
    expect(onToggleTeam).toHaveBeenCalledWith('t1', false)
    fireEvent.click(screen.getByRole('button', { name: 'Unsubscribe from this project' }))
    expect(onToggleSubscription).toHaveBeenCalledOnce()
  })
})

describe('initiative project updates reason', () => {
  it('names the initiative the project is followed through', async () => {
    const { pulseReasonCopy } = await import('./pulse-why-copy')
    const copy = pulseReasonCopy({ kind: 'project', source: { id: 'p', name: 'Apollo', url: '' } }, { type: 'teamProjectUpdates', sourceIds: [], initiativeIds: ['i1'], initiativeNames: ['Launch'] }, { projects: [], initiatives: [], teams: [] }, (s: string) => s)
    expect(copy.plain).toBe('You’re subscribed to project updates for the Launch initiative.')
  })
})
