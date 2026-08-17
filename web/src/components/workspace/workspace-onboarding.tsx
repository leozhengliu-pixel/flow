import { useEffect, useState } from 'react'
import { ChevronDown, ChevronLeft } from 'lucide-react'
import type { AccountBootstrap, BootstrapData } from '@/types/flow'
import { slugifyWorkspace } from './workspace-model'
import { LanguageSelect } from '@/i18n/i18n'

export function WorkspaceOnboarding({ account, onCreate, onBack }: { account: AccountBootstrap; onCreate: (input: { name: string; urlKey: string; region: string }) => Promise<BootstrapData>; onBack: () => void }) {
  const [name, setName] = useState('')
  const [urlKey, setUrlKey] = useState('')
  const [slugEdited, setSlugEdited] = useState(false)
  const [region, setRegion] = useState('us')
  const [regionOpen, setRegionOpen] = useState(false)
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
    <div className="workspace-account-copy"><span>Logged in as</span><strong>{account.viewer.email}</strong></div>
    <form className="workspace-create-form" onSubmit={submit}>
      <h1>Create a workspace</h1>
      <p>Move work forward across teams and agents</p>
      <label><span>Name</span><input autoFocus aria-label="Name" value={name} onChange={event => setName(event.target.value)} /></label>
      <label><span>URL</span><div className="workspace-url-field"><i>flow.app/</i><input aria-label="URL" value={urlKey} onChange={event => { setSlugEdited(true); setUrlKey(slugifyWorkspace(event.target.value)) }} /></div></label>
      <label className="workspace-region-field"><span>Region</span><button type="button" aria-haspopup="listbox" aria-expanded={regionOpen} onClick={() => setRegionOpen(open => !open)}>{region === 'eu' ? 'European Union' : 'United States'}<ChevronDown size={14}/></button>
        {regionOpen && <div role="listbox" className="workspace-region-menu">
          <button type="button" role="option" aria-selected={region === 'us'} onClick={() => { setRegion('us'); setRegionOpen(false) }}>United States</button>
          <button type="button" role="option" aria-selected={region === 'eu'} onClick={() => { setRegion('eu'); setRegionOpen(false) }}>European Union</button>
        </div>}
      </label>
      {error && <div className="workspace-form-error">{error}</div>}
      <button className="workspace-create-submit" disabled={!name.trim() || !urlKey || saving}>{saving ? 'Creating workspace…' : 'Create workspace'}</button>
    </form>
  </main>
}
