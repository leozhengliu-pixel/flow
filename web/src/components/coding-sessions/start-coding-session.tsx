import { useState } from 'react'
import { GitBranch } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import type { CodingEnvironment } from '@/types/coding-sessions'
import './coding-sessions.css'

/** "Which repository?" — the environment picker shown when the suggestion is ambiguous, with optional instructions. */
export function CodingEnvironmentPickerDialog({ environments, suggested, initialPrompt = '', busy, onCancel, onStart }: { environments: CodingEnvironment[]; suggested?: CodingEnvironment; initialPrompt?: string; busy: boolean; onCancel: () => void; onStart: (environmentId: string, prompt: string) => void }) {
  const { t } = useI18n()
  const [selected, setSelected] = useState(suggested?.id ?? environments[0]?.id ?? '')
  const [prompt, setPrompt] = useState(initialPrompt)
  return <Dialog open onOpenChange={open => { if (!open) onCancel() }}>
    <DialogContent className="coding-environment-picker" aria-describedby={undefined} closeLabel={t('Close')}>
      <DialogTitle>{t('Start coding session')}</DialogTitle>
      <form onSubmit={event => { event.preventDefault(); if (selected) onStart(selected, prompt) }}>
        <fieldset>
          <legend>{t('Choose the repository the agent should work in')}</legend>
          {environments.map(environment => <label key={environment.id} className="coding-environment-option" data-selected={selected === environment.id || undefined}>
            <input type="radio" name="coding-environment" value={environment.id} checked={selected === environment.id} onChange={() => setSelected(environment.id)}/>
            <span className="coding-environment-name" data-i18n-ignore>{environment.name}</span>
            <span className="coding-environment-repo" data-i18n-ignore><GitBranch size={12} aria-hidden="true"/>{environment.repository}{environment.hostName && environment.hostName !== 'github.com' && environment.hostName !== 'gitlab.com' ? ` · ${environment.hostName}` : ''}</span>
            {suggested?.id === environment.id && <span className="coding-environment-suggested">{t('Suggested')}</span>}
          </label>)}
        </fieldset>
        <label className="coding-environment-prompt">
          <span>{t('Instructions (optional)')}</span>
          <textarea value={prompt} onChange={event => setPrompt(event.target.value)} placeholder={t('Anything the agent should know…')}/>
        </label>
        <footer>
          <button type="button" className="coding-session-button" onClick={onCancel}>{t('Cancel')}</button>
          <button type="submit" className="coding-session-button is-primary" disabled={busy || !selected}>{t('Start session')}</button>
        </footer>
      </form>
    </DialogContent>
  </Dialog>
}

