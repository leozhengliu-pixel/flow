import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { WorkspaceSettings } from '@/types/flow'
import { updateWorkspacePreferences } from '@/lib/api'
import { FeatureSettingsPage } from './feature-settings'

vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), updateWorkspacePreferences: vi.fn() }))
vi.mock('sonner', () => ({ toast: { error: vi.fn() } }))
beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('flow:locale','en-US') })
const initial = { sessionDurationDays:30, featureFlags:{'customer-requests':true,initiatives:true,pulse:true},featureSettings:{} } as unknown as WorkspaceSettings
function page(onReload = vi.fn(), feature: 'customer-requests'|'pulse'|'initiatives' = 'customer-requests', onNavigateSettings = vi.fn()) {
  return <I18nProvider><FeatureSettingsPage page={feature} data={makeBootstrap({workspaceSettings:initial,customers:[],viewerRole:'admin'})} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={vi.fn()} onNavigateSettings={onNavigateSettings} onReload={onReload}/></I18nProvider>
}
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
