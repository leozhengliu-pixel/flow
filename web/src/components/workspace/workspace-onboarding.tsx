import { useEffect, useState } from 'react'
import { Check, ChevronDown, ChevronLeft } from 'lucide-react'
import type { AccountBootstrap, BootstrapData } from '@/types/flow'
import { slugifyWorkspace } from './workspace-model'
import { LanguageSelect } from '@/i18n/i18n'
import { workspaceRegions } from './workspace-regions'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'

export function WorkspaceOnboarding({ account, onCreate, onBack, onLogout }: { account: AccountBootstrap; onCreate: (input: { name: string; urlKey: string; region: string }) => Promise<BootstrapData>; onBack: () => void; onLogout:()=>Promise<void> }) {
  const [name, setName] = useState('')
  const [urlKey, setUrlKey] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [region, setRegion] = useState(account.workspaceDefaultRegion || 'us')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!slugEdited) setUrlKey(slugifyWorkspace(name))
  }, [name, slugEdited])

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!name.trim() || !urlKey) return
    setSaving(true); setError('')
    try { await onCreate({ name: name.trim(), urlKey, region }) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not create workspace'); setSaving(false) }
  }

  return <main className="workspace-onboarding">
    <LanguageSelect className="workspace-language"/>
    {account.workspaces.length > 0 && <button type="button" className="workspace-back" onClick={onBack}><ChevronLeft size={14}/>Back to Flow</button>}
    <DropdownMenu><DropdownMenuTrigger asChild><button className="workspace-account-copy" type="button"><span>Logged in as</span><strong>{account.viewer.email}</strong></button></DropdownMenuTrigger><DropdownMenuContent align="end" className="workspace-onboarding-account-menu">{account.workspaces.length>0&&<DropdownMenuItem onSelect={onBack}>Back to Flow</DropdownMenuItem>}<DropdownMenuItem onSelect={()=>void onLogout()}>Log out</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
    <form className="workspace-create-form" onSubmit={submit}>
      <h1>Create a workspace</h1>
      <p>Move work forward across teams and agents</p>
      <label><span>Name</span><input autoFocus aria-label="Name" value={name} onChange={event => setName(event.target.value)} /></label>
      <label><span>URL</span><div className="workspace-url-field"><i>flow.app/</i><input aria-label="URL" value={urlKey} onChange={event => { setSlugEdited(true); setUrlKey(slugifyWorkspace(event.target.value)) }} /></div></label>
      {account.workspaceRegionSelectorEnabled && <label className="workspace-region-field"><span>Region</span><DropdownMenu><DropdownMenuTrigger asChild><button aria-label="Region" className="workspace-region-select" role="combobox" type="button">{workspaceRegions.find(option=>option.value===region)?.label}<ChevronDown/></button></DropdownMenuTrigger><DropdownMenuContent align="start" className="workspace-region-menu">{workspaceRegions.map(option=><DropdownMenuItem key={option.value} onSelect={()=>setRegion(option.value)}>{option.label}{region===option.value&&<Check/>}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu></label>}
      {error && <div className="workspace-form-error">{error}</div>}
      <button className="workspace-create-submit" disabled={!name.trim() || !urlKey || saving}>{saving ? 'Creating workspace…' : 'Create workspace'}</button>
    </form>
  </main>
}
