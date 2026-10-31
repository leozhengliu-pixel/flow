import { useMemo, useState } from 'react'
import { matchesSearch } from '@/lib/search-match'
import { Search } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { useUserPreferences } from '@/lib/runtime-preferences'

type Shortcut = { label: string; keys: string[] }

function shortcutGroups(sendCommentKeys: string[]): { title: string; shortcuts: Shortcut[] }[] {
  return [
    { title: 'General', shortcuts: [
      { label: 'Open command menu', keys: ['⌘', 'K'] },
      { label: 'Save or submit', keys: ['⌘', 'Enter'] },
      { label: 'Send comment', keys: sendCommentKeys },
      { label: 'Back', keys: ['Esc'] },
      { label: 'Open search', keys: ['/'] },
      { label: 'Open help menu', keys: ['?'] },
      { label: 'View keyboard shortcuts', keys: ['⌘', '/'] },
      { label: 'Switch workspace', keys: ['O', 'then', 'W'] },
      { label: 'Log out', keys: ['⌥', '⇧', 'Q'] },
      { label: 'Ask Flow', keys: ['⌘', 'J'] },
    ] },
    { title: 'Navigation', shortcuts: [
      { label: 'Toggle left sidebar', keys: ['['] },
      { label: 'Toggle right sidebar', keys: [']'] },
      { label: 'Open issue', keys: ['O', 'then', 'I'] },
      { label: 'Open parent issue', keys: ['⌘', '⇧', '↑'] },
      { label: 'Go to team', keys: ['Ctrl', '⇧', '1-9'] },
      { label: 'Go to inbox', keys: ['G', 'then', 'I'] },
      { label: 'Go to Agent', keys: ['G', 'then', 'J'] },
      { label: 'Go to my issues', keys: ['G', 'then', 'M'] },
      { label: 'Go to reviews', keys: ['G', 'then', 'R'] },
      { label: 'Go to settings', keys: ['G', 'then', 'S'] },
      { label: 'Go to triage', keys: ['G', 'then', 'T'] },
      { label: 'Go to drafts', keys: ['G', 'then', 'D'] },
      { label: 'Go to active issues', keys: ['G', 'then', 'A'] },
      { label: 'Go to backlog', keys: ['G', 'then', 'B'] },
      { label: 'Go to cycles', keys: ['G', 'then', 'C'] },
      { label: 'Go to projects', keys: ['G', 'then', 'P'] },
      { label: 'Go to pulse', keys: ['G', 'then', 'F'] },
      { label: 'Go to customers', keys: ['G', 'then', 'Q'] },
      { label: 'Open customer…', keys: ['O', 'then', 'Q'] },
      { label: 'Open document…', keys: ['O', 'then', 'D'] },
    ] },
    { title: 'Issues', shortcuts: [
      { label: 'New issue', keys: ['C'] },
    ] },
    { title: 'Triage', shortcuts: [
      { label: 'Accept issue', keys: ['1'] },
      { label: 'Decline issue', keys: ['2'] },
      { label: 'Mark issue as duplicate of', keys: ['3'] },
      { label: 'Snooze issue until', keys: ['H'] },
      { label: 'Next issue', keys: ['J'] },
      { label: 'Previous issue', keys: ['K'] },
      { label: 'Back to triage', keys: ['Esc'] },
    ] },
    { title: 'Projects', shortcuts: [
      { label: 'New project', keys: ['N', 'then', 'P'] },
    ] },
    { title: 'Initiatives', shortcuts: [
      { label: 'New initiative', keys: ['N', 'then', 'I'] },
    ] },
  ]
}

export function KeyboardShortcutsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const { sendComments } = useUserPreferences()
  const [query, setQuery] = useState('')
  const filtered = useMemo(() => shortcutGroups(sendComments === '⌘ Enter' ? ['⌘', 'Enter'] : ['Enter']).map(group => ({ ...group, shortcuts: group.shortcuts.filter(shortcut =>
    matchesSearch(query, [shortcut.label, shortcut.keys.join(' ')], t),
  ) })).filter(group => group.shortcuts.length), [query, sendComments, t])

  return <Dialog open={open} onOpenChange={next => { onOpenChange(next); if (!next) setQuery('') }}>
    <DialogContent aria-describedby={undefined} className="sidebar-shortcuts-dialog" closeLabel={t('Close dialog')}>
      <DialogTitle>{t('Keyboard Shortcuts')}</DialogTitle>
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
