import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, WorkspaceSettings } from '@/types/flow'
import { updateWorkspacePreferences } from '@/lib/api'
import { FeatureSettingsPage } from './feature-settings'

vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), updateWorkspacePreferences: vi.fn() }))
vi.mock('sonner', () => ({ toast: { error: vi.fn() } }))
beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('flow:locale','en-US') })
const initial = { sessionDurationDays:30, featureFlags:{'customer-requests':true,initiatives:true,pulse:true},featureSettings:{} } as unknown as WorkspaceSettings
function page(onReload = vi.fn(), feature: 'customer-requests'|'pulse'|'initiatives'|'ai'|'asks' = 'customer-requests', onNavigateSettings = vi.fn(), overrides: Partial<BootstrapData> = {}) {
  return <I18nProvider><FeatureSettingsPage page={feature} data={makeBootstrap({workspaceSettings:initial,customers:[],integrationConnections:[],emailIntakeAddresses:[],viewerRole:'admin',...overrides})} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={vi.fn()} onNavigateSettings={onNavigateSettings} onReload={onReload}/></I18nProvider>
}

it.each(['admin', 'owner'] as const)('lets %s turn Asks off and on without overwriting customer requests or reloading the workspace', async viewerRole => {
  let complete!: (value: WorkspaceSettings) => void
  vi.mocked(updateWorkspacePreferences).mockImplementationOnce(() => new Promise(resolve => { complete = resolve }))
  const reload = vi.fn()
  render(page(reload, 'asks', vi.fn(), { viewerRole }))
  const toggle = screen.getByRole('checkbox', { name: 'Enable Asks' })
  expect(toggle).toBeChecked()
  fireEvent.click(toggle)
  expect(toggle).not.toBeChecked()
  expect(toggle).toBeDisabled()
  expect(updateWorkspacePreferences).toHaveBeenCalledWith({ featureFlags: { asks: false } }, 'workspace')
  await act(async () => complete({ ...initial, featureFlags: { ...initial.featureFlags, asks: false } }))
  expect(toggle).toBeEnabled()
  vi.mocked(updateWorkspacePreferences).mockResolvedValueOnce({ ...initial, featureFlags: { ...initial.featureFlags, asks: true } })
  fireEvent.click(toggle)
  await waitFor(() => expect(toggle).toBeEnabled())
  expect(toggle).toBeChecked()
  expect(updateWorkspacePreferences).toHaveBeenLastCalledWith({ featureFlags: { asks: true } }, 'workspace')
  expect(reload).not.toHaveBeenCalled()
})

it('restores the saved Asks flag and rolls back a rejected change', async () => {
  vi.mocked(updateWorkspacePreferences).mockRejectedValueOnce(new Error('Save denied'))
  render(page(vi.fn(), 'asks', vi.fn(), { workspaceSettings: { ...initial, featureFlags: { asks: false, 'customer-requests': true } } }))
  const toggle = screen.getByRole('checkbox', { name: 'Enable Asks' })
  expect(toggle).not.toBeChecked()
  fireEvent.click(toggle)
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Save denied'))
  expect(toggle).not.toBeChecked()
  expect(toggle).toBeEnabled()
})

it.each(['member', 'guest'] as const)('keeps Asks and its integration settings read-only for %s', viewerRole => {
  render(page(vi.fn(), 'asks', vi.fn(), { viewerRole }))
  const toggle = screen.getByRole('checkbox', { name: 'Enable Asks' })
  expect(toggle).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Connect workspace' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Add email' })).toBeDisabled()
  fireEvent.click(toggle)
  expect(updateWorkspacePreferences).not.toHaveBeenCalled()
})

it('localizes the Asks toggle and description', () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  render(page(vi.fn(), 'asks'))
  expect(screen.getByRole('checkbox', { name: '启用请求' })).toBeChecked()
  expect(screen.getByText('允许成员通过请求创建事项')).toBeVisible()
})

it('enables and configures Triage Intelligence behavior', async () => {
  const user = userEvent.setup()
  const enabled = {
    ...initial,
    featureFlags: { ...initial.featureFlags, 'triage-intelligence': true },
    featureSettings: {
      ...initial.featureSettings,
      triageIntelligence: {
        assigneeAction: 'suggest', projectAction: 'suggest', labelAction: 'suggest',
        teamAction: 'suggest', duplicateAction: 'suggest', relatedAction: 'suggest',
      },
    },
  } as WorkspaceSettings
  vi.mocked(updateWorkspacePreferences).mockResolvedValueOnce(enabled)
  render(page(vi.fn(), 'ai'))

  await user.click(screen.getByRole('checkbox', { name: 'Enable Triage Intelligence' }))
  await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenCalledWith({ featureFlags: { 'triage-intelligence': true } }, 'workspace'))

  vi.mocked(updateWorkspacePreferences).mockResolvedValueOnce({
    ...enabled,
    featureSettings: {
      ...enabled.featureSettings,
      triageIntelligence: { ...enabled.featureSettings.triageIntelligence, projectAction: 'auto' },
    },
  })
  await user.click(screen.getByRole('combobox', { name: 'Project is suggested' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Apply automatically' }))
  await waitFor(() => expect(updateWorkspacePreferences).toHaveBeenLastCalledWith({
    featureSettings: {
      triageIntelligence: {
        assigneeAction: 'suggest', projectAction: 'auto', labelAction: 'suggest',
        teamAction: 'suggest', duplicateAction: 'suggest', relatedAction: 'suggest',
      },
    },
  }, 'workspace'))
})
it('turns off immediately and applies the response without reloading a large workspace',async()=>{
  let complete!: (value: WorkspaceSettings) => void
  vi.mocked(updateWorkspacePreferences).mockImplementationOnce(()=>new Promise(resolve=>{complete=resolve}))
  const reload=vi.fn(()=>new Promise<void>(()=>{}))
  render(page(reload))
  const toggle=screen.getByRole('checkbox',{name:'Enable Customer requests'})
  fireEvent.click(toggle)
  expect(toggle).not.toBeChecked()
  expect(toggle).toBeDisabled()
  expect(updateWorkspacePreferences).toHaveBeenCalledWith({featureFlags:{'customer-requests':false}},'workspace')
  await act(async()=>complete({...initial,featureFlags:{...initial.featureFlags,'customer-requests':false}}))
  expect(toggle).not.toBeChecked()
  expect(toggle).toBeEnabled()
  expect(reload).not.toHaveBeenCalled()
  vi.mocked(updateWorkspacePreferences).mockResolvedValueOnce(initial)
  fireEvent.click(toggle)
  await waitFor(()=>expect(toggle).toBeChecked())
})
it('rolls back and reports a failed save instead of leaving a false disabled state',async()=>{
  vi.mocked(updateWorkspacePreferences).mockRejectedValueOnce(new Error('Save denied'))
  render(page())
  const toggle=screen.getByRole('checkbox',{name:'Enable Customer requests'})
  fireEvent.click(toggle)
  await waitFor(()=>expect(toast.error).toHaveBeenCalledWith('Save denied'))
  expect(toggle).toBeChecked()
  expect(toggle).toBeEnabled()
})

it('disables Pulse without requesting another workspace snapshot',async()=>{
  vi.mocked(updateWorkspacePreferences).mockResolvedValueOnce({...initial,featureFlags:{...initial.featureFlags,pulse:false}})
  const reload=vi.fn()
  render(page(reload,'pulse'))
  const toggle=screen.getByRole('checkbox',{name:'Enable Pulse'})
  fireEvent.click(toggle)
  await waitFor(()=>expect(toggle).toBeEnabled())
  expect(toggle).not.toBeChecked()
  expect(updateWorkspacePreferences).toHaveBeenCalledWith({featureFlags:{pulse:false}},'workspace')
  expect(reload).not.toHaveBeenCalled()
})

it('edits the complete initiative update schedule and opens initiative labels',async()=>{
  const user=userEvent.setup()
  const updated = {...initial,featureSettings:{...initial.featureSettings,initiativeUpdateSchedule:'biweekly',initiativeUpdateFrequencyWeeks:2,initiativeUpdateWeekday:4,initiativeUpdateHour:14}}
  vi.mocked(updateWorkspacePreferences).mockResolvedValueOnce(updated)
  const onNavigateSettings=vi.fn()
  render(page(vi.fn(),'initiatives',onNavigateSettings))

  await user.click(screen.getByRole('button',{name:'Edit'}))
  const frequency=screen.getByRole('combobox',{name:'Update frequency'})
  await user.click(frequency)
  await user.click(await screen.findByRole('menuitem',{name:'Every 2 weeks'}))
  const weekday=screen.getByRole('combobox',{name:'Update weekday'})
  await user.click(weekday)
  await user.click(await screen.findByRole('menuitem',{name:'Friday'}))
  await user.click(screen.getByRole('button',{name:'Save'}))

  await waitFor(()=>expect(updateWorkspacePreferences).toHaveBeenCalledWith({featureSettings:{initiativeUpdateSchedule:'biweekly',initiativeUpdateFrequencyWeeks:2,initiativeUpdateWeekday:4,initiativeUpdateHour:14}},'workspace'))
  await user.click(screen.getByRole('button',{name:/Initiative labels/}))
  expect(onNavigateSettings).toHaveBeenCalledWith('initiative-labels')
})

it('renders initiative settings over one million labels without walking them during interaction',()=>{
  const label={id:'initiative-label',name:'Strategy',color:'#123456',resourceType:'initiative' as const}
  const data=makeBootstrap({workspaceSettings:initial,customers:[],viewerRole:'admin'})
  data.labels=Array.from({length:1_000_000},()=>label)
  const start=performance.now()
  render(<I18nProvider><FeatureSettingsPage page="initiatives" data={data} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={vi.fn()} onNavigateSettings={vi.fn()} onReload={vi.fn()}/></I18nProvider>)
  const elapsed=performance.now()-start

  expect(screen.getByText('1000000 labels')).toBeVisible()
  expect(elapsed).toBeLessThan(250)
})

it('gates Code Intelligence behind GitHub code access and shows repository policy when granted', () => {
  const open = vi.fn()
  const withoutAccess = makeBootstrap({
    workspaceSettings: initial,
    viewerRole: 'admin',
    integrationConnections: [
      {
        id: 'gh1',
        provider: 'github',
        name: 'GitHub',
        status: 'connected',
        scopes: [],
        channels: [],
        linkbackEnabled: false,
        deliveryAttempts: 0,
        connectedBy: 'u1',
        createdAt: '2026-01-01T00:00:00Z',
        updatedAt: '2026-01-01T00:00:00Z',
        config: {},
      },
    ] as never[],
  })
  const { rerender } = render(
    <I18nProvider>
      <FeatureSettingsPage page="ai" data={withoutAccess} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={open} onReload={vi.fn()} />
    </I18nProvider>,
  )
  expect(screen.getByRole('heading', { name: 'Code Intelligence' })).toBeVisible()
  expect(screen.getByText('Needs code access via GitHub integration')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Enable code access' })).toBeVisible()
  expect(screen.queryByRole('checkbox', { name: 'Enable Code Intelligence' })).not.toBeInTheDocument()

  const withAccess = makeBootstrap({
    workspaceSettings: initial,
    viewerRole: 'admin',
    integrationConnections: [
      {
        ...withoutAccess.integrationConnections[0],
        config: { codeAccess: 'true' },
      },
    ] as never[],
  })
  rerender(
    <I18nProvider>
      <FeatureSettingsPage page="ai" data={withAccess} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={open} onReload={vi.fn()} />
    </I18nProvider>,
  )
  expect(screen.getByRole('checkbox', { name: 'Enable Code Intelligence' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Repository access' })).toBeVisible()
  expect(screen.getByRole('checkbox', { name: 'Extend access to all members' })).toBeDisabled()
})
