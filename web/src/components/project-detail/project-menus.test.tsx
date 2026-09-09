import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { Initiative, Project, Subscription } from '@/types/flow'
import { ProjectPropertiesMenu } from './project-properties-menu'
import { ProjectActionsMenu } from './project-header-menus'

const initiative = {id:'initiative-1',name:'Platform reliability',status:'active',color:'#4caf80',icon:'Initiative'} as Initiative

function properties(overrides: Partial<Project> = {}) {
  const data = makeBootstrap()
  const save = vi.fn().mockResolvedValue(undefined)
  const updateProject = vi.fn().mockResolvedValue(project)
  const next = {...project,id:'project-2',slugId:'second',name:'Next project',memberIds:[],lead:undefined}
  render(<I18nProvider><ProjectPropertiesMenu project={{...project,...overrides}} projects={[project,next]} projectRelations={[]} initiatives={[initiative]} users={data.users} viewer={viewer} labels={[]} labelGroups={[]} save={save} onUpdateProject={updateProject}/></I18nProvider>)
  return {save,updateProject,next}
}

function actions() {
  const callbacks = {onDelete:vi.fn(),onFavorite:vi.fn(),onRemind:vi.fn().mockResolvedValue(undefined),onShowActivity:vi.fn(),onShowHistory:vi.fn(),onShowNotifications:vi.fn(),onSetEvents:vi.fn().mockResolvedValue(undefined),onUpdateSchedule:vi.fn().mockResolvedValue(undefined)}
  render(<I18nProvider><ProjectActionsMenu project={project} favorited={false} subscription={{events:['projectUpdate']} as Subscription} {...callbacks}/></I18nProvider>)
  return callbacks
}

beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver',class { observe() {} unobserve() {} disconnect() {} }) })
afterEach(() => vi.unstubAllGlobals())

describe('project properties menus', () => {
  it('shows missing properties and opens the shared members picker on hover', async () => {
    const user = userEvent.setup(); const {save} = properties()
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    expect(screen.getByRole('menuitem',{name:'Start date…'})).toBeVisible()
    await user.hover(screen.getByRole('menuitem',{name:/Members/}))
    const input = await screen.findByRole('textbox',{name:'Change members…'})
    await user.type(input,teammate.displayName)
    await user.click(screen.getByRole('option',{name:new RegExp(teammate.displayName)}))
    expect(save).toHaveBeenCalledWith({memberIds:[viewer.id,teammate.id]})
    expect(screen.getByRole('textbox',{name:'Change members…'})).toBeVisible()
  })

  it('does not offer adding properties which are already present', async () => {
    const user = userEvent.setup(); properties({memberIds:[viewer.id,teammate.id],startDate:'2026-09-01'})
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    expect(screen.queryByRole('menuitem',{name:/Members/})).not.toBeInTheDocument()
    expect(screen.queryByRole('menuitem',{name:'Start date…'})).not.toBeInTheDocument()
    expect(screen.getByRole('menuitem',{name:'Dependencies'})).toBeVisible()
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
    expect(screen.getByRole('menuitem',{name:'Dependencies'})).toBeVisible()
  })

  it.each([['Blocked by','blocked_by'],['Blocking','blocks']] as const)('creates the correct dependency direction through %s', async (direction,type) => {
    const user = userEvent.setup(); const {save,next} = properties()
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    await user.hover(screen.getByRole('menuitem',{name:'Dependencies'}))
    await user.hover(await screen.findByRole('menuitem',{name:direction}))
    await user.click(await screen.findByRole('menuitemcheckbox',{name:next.name}))
    expect(save).toHaveBeenCalledWith({dependencyRelations:[{projectId:next.id,type}]})
    expect(screen.queryByRole('menuitemcheckbox',{name:project.name})).not.toBeInTheDocument()
  })

  it('opens the existing date picker from the missing start-date action', async () => {
    const user = userEvent.setup(); properties()
    await user.click(screen.getByRole('button',{name:'More project properties'}))
    await user.click(screen.getByRole('menuitem',{name:'Start date…'}))
    expect(await screen.findByRole('tablist',{name:'Date precision'})).toBeVisible()
    expect(screen.queryByRole('menuitem',{name:'Dependencies'})).not.toBeInTheDocument()
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
})
