import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { Initiative, Project, Subscription } from '@/types/flow'
import { ProjectPropertiesMenu } from './project-properties-menu'
import { ProjectActionsMenu, ProjectNotificationMenu } from './project-header-menus'
import { resetPulseSessionChoices } from '@/lib/pulse-subscriptions'
import { TooltipProvider } from '@/components/ui/tooltip'

const pulseMocks = vi.hoisted(() => ({ setPulseSubscription: vi.fn(), request: vi.fn() }))
vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), setPulseSubscription: pulseMocks.setPulseSubscription }))
vi.mock('@/lib/api-client', async original => ({ ...await original<typeof import('@/lib/api-client')>(), request: pulseMocks.request }))

const initiative = {id:'initiative-1',name:'Platform reliability',status:'active',color:'#4caf80',icon:'Initiative'} as Initiative

function properties(overrides: Partial<Project> = {}) {
  const data = makeBootstrap()
  const save = vi.fn().mockResolvedValue(undefined)
  const updateProject = vi.fn().mockResolvedValue(project)
  const next = {...project,id:'project-2',slugId:'second',name:'Next project',memberIds:[],lead:undefined}
  render(<I18nProvider><ProjectPropertiesMenu project={{...project,...overrides}} projects={[project,next]} projectRelations={[]} initiatives={[initiative]} users={data.users} viewer={viewer} labels={[]} labelGroups={[]} save={save} onUpdateProject={updateProject}/></I18nProvider>)
  return {save,updateProject,next}
}

function actions(pulseSubscribed?: boolean) {
  const callbacks = {onDelete:vi.fn(),onFavorite:vi.fn(),onRemind:vi.fn().mockResolvedValue(undefined),onShowActivity:vi.fn(),onShowHistory:vi.fn(),onShowNotifications:vi.fn(),onSetEvents:vi.fn().mockResolvedValue(undefined),onUpdateSchedule:vi.fn().mockResolvedValue(undefined)}
  render(<I18nProvider><ProjectActionsMenu project={project} favorited={false} subscription={{events:['projectUpdate']} as Subscription} pulseSubscribed={pulseSubscribed} {...callbacks}/></I18nProvider>)
  return callbacks
}

beforeEach(() => { localStorage.clear(); resetPulseSessionChoices(); pulseMocks.setPulseSubscription.mockReset(); pulseMocks.request.mockReset().mockReturnValue(new Promise(() => undefined)); vi.stubGlobal('ResizeObserver',class { observe() {} unobserve() {} disconnect() {} }) })
afterEach(() => vi.unstubAllGlobals())

describe('project properties menus', () => {
  it('matches Linear\'s item order and shortcut hints', async () => {
    const user = userEvent.setup(); properties()
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    const menu = screen.getByRole('menu')
    expect(menu).toHaveClass('project-action-menu--properties')
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['MembersP then M▶','LabelsP then L▶','InitiativesP then N▶','Connect existing Slack channel…'])
    expect(screen.queryByRole('menuitem',{name:'Start date…'})).not.toBeInTheDocument()
    expect(screen.queryByRole('menuitem',{name:'Dependencies'})).not.toBeInTheDocument()
  })

  it('only offers initiatives when the workspace has one', async () => {
    const user = userEvent.setup(); const data = makeBootstrap()
    render(<I18nProvider><ProjectPropertiesMenu project={project} projects={[project]} projectRelations={[]} initiatives={[]} users={data.users} viewer={viewer} labels={[]} labelGroups={[]} save={vi.fn()} onUpdateProject={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    const items = within(screen.getByRole('menu')).getAllByRole('menuitem').map(item => item.textContent)
    expect(items.some(item => item?.startsWith('Initiatives'))).toBe(false)
    // Linear has no Customer request… entry here: the overview always shows the Customers row.
    expect(items).not.toContain('Customer request…')
  })

  it('opens the shared members picker on hover', async () => {
    const user = userEvent.setup(); const {save} = properties()
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    await user.hover(screen.getByRole('menuitem',{name:/Members/}))
    const input = await screen.findByRole('textbox',{name:'Change members…'})
    expect(screen.queryByText('Users from the project team')).toBeNull()
    await user.type(input,teammate.displayName)
    await user.click(screen.getByRole('option',{name:new RegExp(teammate.displayName)}))
    expect(save).toHaveBeenCalledWith({memberIds:[viewer.id,teammate.id]})
    expect(screen.getByRole('textbox',{name:'Change members…'})).toBeVisible()
  })

  it('does not offer adding properties which are already present', async () => {
    const user = userEvent.setup(); properties({memberIds:[viewer.id,teammate.id]})
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    expect(screen.queryByRole('menuitem',{name:/Members/})).not.toBeInTheDocument()
    expect(screen.getByRole('menuitem',{name:/Labels/})).toBeVisible()
  })

  it('clears the lead when removing that person from project members', async () => {
    const user = userEvent.setup(); const {save} = properties({memberIds:[]})
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    await user.hover(screen.getByRole('menuitem',{name:'Members'}))
    await user.click(await screen.findByRole('option',{name:/Viewer/}))
    expect(save).toHaveBeenCalledWith({memberIds:[],leadId:''})
  })

  it('searches and toggles initiatives through the shared multi-select picker', async () => {
    const user = userEvent.setup(); const {save} = properties()
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    await user.hover(screen.getByRole('menuitem',{name:/Initiatives/}))
    const input = await screen.findByRole('textbox',{name:'Change initiatives…'})
    await user.type(input,'reliability')
    expect(screen.queryByText('No options')).not.toBeInTheDocument()
    await user.click(screen.getByRole('option',{name:initiative.name}))
    expect(save).toHaveBeenCalledWith({initiatives:[initiative.id]})
    await user.keyboard('{ArrowLeft}')
    await waitFor(() => expect(screen.queryByRole('textbox',{name:'Change initiatives…'})).not.toBeInTheDocument())
    expect(screen.getByRole('menuitem',{name:/Labels/})).toBeVisible()
  })
})

describe('project header menu', () => {
  it('filters translated actions without orphan separators and copies the title', async () => {
    const user = userEvent.setup(); actions()
    const write = vi.spyOn(navigator.clipboard,'writeText').mockResolvedValue()
    await user.click(screen.getByRole('button',{name:'Project actions'}))
    await user.type(screen.getByRole('textbox',{name:'Filter project actions'}),'Copy')
    expect(screen.queryByRole('separator')).not.toBeInTheDocument()
    await user.hover(screen.getByRole('menuitem',{name:'Copy'}))
    await user.click(await screen.findByRole('menuitem',{name:'Copy title'}))
    expect(write).toHaveBeenCalledWith(project.name)
  })

  it('toggles individual subscriptions while preserving other event types', async () => {
    const user = userEvent.setup(); const {onSetEvents} = actions()
    await user.click(screen.getByRole('button',{name:'Project actions'}))
    await user.hover(screen.getByRole('menuitem',{name:'Subscribe'}))
    const row = await screen.findByRole('menuitemcheckbox',{name:'An issue is added to the project'})
    await user.click(row)
    expect(onSetEvents).toHaveBeenLastCalledWith(['projectUpdate','issueAdded'])
    expect(row).toHaveAttribute('aria-checked','true')
    await user.click(row)
    expect(onSetEvents).toHaveBeenLastCalledWith(['projectUpdate'])
    expect(row).toHaveAttribute('aria-checked','false')
  })

  it('opens an update schedule dialog rather than the notification popover', async () => {
    const user = userEvent.setup(); const {onShowNotifications,onUpdateSchedule} = actions()
    await user.click(screen.getByRole('button',{name:'Project actions'}))
    await user.click(screen.getByRole('menuitem',{name:'Change update schedule…'}))
    const dialog = await screen.findByRole('dialog',{name:'Update schedule'})
    expect(onShowNotifications).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button',{name:'Save'}))
    expect(onUpdateSchedule).toHaveBeenCalledWith(expect.objectContaining({mode:'default'}))
  })

  it('saves a custom update cadence and its weekday and local time', async () => {
    const user = userEvent.setup(); const {onUpdateSchedule} = actions()
    await user.click(screen.getByRole('button',{name:'Project actions'}))
    await user.click(screen.getByRole('menuitem',{name:'Change update schedule…'}))
    await user.click(screen.getByRole('radio',{name:'Custom schedule'}))
    await user.click(screen.getByRole('combobox',{name:'Frequency'}))
    await user.click(screen.getByRole('option',{name:'Every 3 weeks'}))
    await user.click(screen.getByRole('combobox',{name:'Day of week'}))
    await user.click(screen.getByRole('option',{name:'Monday'}))
    await user.click(screen.getByRole('button',{name:'Save'}))
    expect(onUpdateSchedule).toHaveBeenCalledWith(expect.objectContaining({mode:'custom',frequencyDays:21,weekday:1,hour:14}))
  })

  it('matches Linear project actions without an insights item', async () => {
    const user = userEvent.setup(); actions()
    await user.click(screen.getByRole('button',{name:'Project actions'}))
    const labels = screen.getAllByRole('menuitem').map(item => item.querySelector('.project-menu-label')?.textContent ?? item.textContent)
    expect(labels).toEqual(['Copy','Favorite','Subscribe','Remind me','Change update schedule…','Show description history','Show updates and activity','Delete'])
    expect(screen.queryByRole('menuitem',{name:/project insights/})).not.toBeInTheDocument()
  })

  it('subscribes to project updates in Pulse through the Pulse API, apart from inbox events', async () => {
    const user = userEvent.setup(); const {onSetEvents} = actions(true)
    pulseMocks.setPulseSubscription.mockResolvedValue({subscribed:false})
    await user.click(screen.getByRole('button',{name:'Project actions'}))
    await user.hover(screen.getByRole('menuitem',{name:'Subscribe'}))
    expect(await screen.findByText('Pulse updates')).toBeVisible()
    const pulse = screen.getByRole('menuitemcheckbox',{name:'Subscribe to project updates'})
    expect(pulse).toHaveAttribute('aria-checked','true')
    await user.click(pulse)
    await waitFor(() => expect(screen.getByRole('menuitemcheckbox',{name:'Subscribe to project updates'})).toHaveAttribute('aria-checked','false'))
    expect(pulseMocks.setPulseSubscription).toHaveBeenCalledWith('project',project.id,false)
    expect(onSetEvents).not.toHaveBeenCalled()
    await user.click(screen.getByRole('menuitemcheckbox',{name:'An issue is added to the project'}))
    expect(onSetEvents).toHaveBeenLastCalledWith(['projectUpdate','issueAdded'])
  })

  it('rolls back the Pulse checkbox in the notification popover when saving fails', async () => {
    const user = userEvent.setup()
    pulseMocks.setPulseSubscription.mockRejectedValue(new Error('Offline'))
    const toastError = vi.spyOn((await import('sonner')).toast, 'error')
    render(<I18nProvider><TooltipProvider><ProjectNotificationMenu open onOpenChange={vi.fn()} project={project} subscription={{events:['pulse']} as Subscription} onSetEvents={vi.fn()} onUpdate={vi.fn()}/></TooltipProvider></I18nProvider>)
    const pulse = screen.getByRole('checkbox',{name:'Subscribe to project updates'})
    expect(pulse).toBeChecked()
    expect(pulseMocks.request).toHaveBeenCalledWith(`/api/pulse/subscriptions/project/${project.id}`,expect.anything())
    await user.click(pulse)
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Could not update Pulse subscription',{description:'Offline'}))
    expect(pulseMocks.setPulseSubscription).toHaveBeenCalledWith('project',project.id,false)
    expect(screen.getByRole('checkbox',{name:'Subscribe to project updates'})).toBeChecked()
  })
})
