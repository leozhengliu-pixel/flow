import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'

type Shortcut = { label: string; keys: string[] }

const groups: { title: string; shortcuts: Shortcut[] }[] = [
  { title: 'General', shortcuts: [
    { label: 'Open command menu', keys: ['⌘', 'K'] },
    { label: 'Open search', keys: ['/'] },
    { label: 'View keyboard shortcuts', keys: ['⌘', '/'] },
  ] },
  { title: 'Navigation', shortcuts: [
    { label: 'Toggle left sidebar', keys: ['['] },
    { label: 'Go to inbox', keys: ['G', 'then', 'I'] },
    { label: 'Go to Agent', keys: ['G', 'then', 'J'] },
    { label: 'Go to my issues', keys: ['G', 'then', 'M'] },
    { label: 'Go to reviews', keys: ['G', 'then', 'R'] },
    { label: 'Go to settings', keys: ['G', 'then', 'S'] },
  ] },
  { title: 'Issues', shortcuts: [
    { label: 'New issue', keys: ['C'] },
  ] },
  { title: 'Projects', shortcuts: [
    { label: 'New project', keys: ['N', 'then', 'P'] },
  ] },
  { title: 'Initiatives', shortcuts: [
    { label: 'New initiative', keys: ['N', 'then', 'I'] },
  ] },
]

export function KeyboardShortcutsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const filtered = useMemo(() => groups.map(group => ({ ...group, shortcuts: group.shortcuts.filter(shortcut =>
    `${shortcut.label} ${shortcut.keys.join(' ')}`.toLowerCase().includes(query.trim().toLowerCase()),
  ) })).filter(group => group.shortcuts.length), [query])

  return <Dialog open={open} onOpenChange={next => { onOpenChange(next); if (!next) setQuery('') }}>
    <DialogContent aria-describedby={undefined} className="sidebar-shortcuts-dialog" closeLabel={t('Close dialog')}>
      <DialogTitle>{t('Keyboard shortcuts')}</DialogTitle>
      <div className="sidebar-shortcuts-search"><Search size={15}/><input aria-label={t('Search shortcuts')} autoFocus placeholder={t('Search shortcuts')} value={query} onChange={event => setQuery(event.target.value)}/></div>
      <div className="sidebar-shortcuts-scroll">
        {filtered.length ? filtered.map(group => <section key={group.title}>
          <h3>{t(group.title)}</h3>
          <dl>{group.shortcuts.map(shortcut => <div key={shortcut.label}>
            <dt>{t(shortcut.label)}</dt>
            <dd>{shortcut.keys.map((key, index) => key === 'then' ? <small key={index}>{t('then')}</small> : <kbd key={index}>{key}</kbd>)}</dd>
          </div>)}</dl>
        </section>) : <p className="sidebar-shortcuts-empty">{t('No shortcuts found')}</p>}
      </div>
    </DialogContent>
  </Dialog>
}
