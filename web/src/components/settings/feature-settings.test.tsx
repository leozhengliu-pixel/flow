import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
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
function page(onReload = vi.fn(), feature: 'customer-requests'|'pulse' = 'customer-requests') {
  return <I18nProvider><FeatureSettingsPage page={feature} data={makeBootstrap({workspaceSettings:initial,customers:[],viewerRole:'admin'})} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={vi.fn()} onReload={onReload}/></I18nProvider>
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
