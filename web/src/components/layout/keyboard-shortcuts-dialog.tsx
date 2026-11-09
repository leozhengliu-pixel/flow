import { useMemo, useState } from 'react'
import { matchesSearch } from '@/lib/search-match'
import { Search } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { useUserPreferences } from '@/lib/runtime-preferences'
import { useDesktopLayout } from '@/hooks/use-desktop-layout'

type Shortcut = { label: string; keys: string[] }

function shortcutGroups(sendCommentKeys: string[], desktop: boolean): { title: string; shortcuts: Shortcut[] }[] {
  const groups = [
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
  if (desktop) {
    groups.find(group => group.title === 'Navigation')!.shortcuts.push(
      { label: 'Open team archive', keys: ['G', 'then', 'X'] },
      { label: 'Go to all issues', keys: ['G', 'then', 'E'] },
      { label: 'Go to Loops', keys: ['G', 'then', 'L'] },
      { label: 'Go to current cycle', keys: ['G', 'then', 'V'] },
      { label: 'Go to upcoming cycle', keys: ['G', 'then', 'W'] },
      { label: 'Go to initiatives', keys: ['G', 'then', 'N'] },
      { label: 'Go to dashboards', keys: ['G', 'then', '-'] },
      { label: 'Open a favorite', keys: ['O', 'then', 'F'] },
      { label: 'Open a project', keys: ['O', 'then', 'P'] },
      { label: 'Open a cycle', keys: ['O', 'then', 'C'] },
      { label: 'Open a team', keys: ['O', 'then', 'T'] },
      { label: 'Open a user', keys: ['O', 'then', 'U'] },
      { label: 'Open view', keys: ['O', 'then', 'V'] },
      { label: 'Open initiative', keys: ['O', 'then', 'N'] },
      { label: 'Open link from last toast', keys: ['⌘', '⌥', 'O'] },
      { label: 'Dismiss all toasts', keys: ['⌘', '⌥', 'X'] },
      { label: 'Show display options', keys: ['⇧', 'V'] },
    )
    groups.find(group => group.title === 'Issues')!.shortcuts.push(
      { label: 'New issue in full screen view', keys: ['V'] },
      { label: 'Assign issue to user', keys: ['A'] },
      { label: 'Assign to me', keys: ['I'] },
      { label: 'Change labels', keys: ['L'] },
      { label: 'Change issue status', keys: ['S'] },
      { label: 'Change priority', keys: ['P'] },
      { label: 'Change estimate', keys: ['⇧', 'E'] },
      { label: 'Set due date', keys: ['⇧', 'D'] },
      { label: 'Rename', keys: ['⇧', 'R'] },
      { label: 'Focus issue description input', keys: ['Ctrl', '⇧', 'I'] },
      { label: 'Mark as favorite', keys: ['⌥', 'F'] },
      { label: 'Remind about issue', keys: ['⇧', 'H'] },
      { label: 'Move to another team', keys: ['⌘', '⇧', 'M'] },
      { label: 'Delete issue', keys: ['⌘', '⌫'] },
      { label: 'Subscribe to issue', keys: ['⇧', 'S'] },
      { label: 'Manage issue subscribers', keys: ['⌘', '⇧', 'S'] },
      { label: 'Mark as blocked', keys: ['M', 'then', 'B'] },
      { label: 'Mark as blocking', keys: ['M', 'then', 'X'] },
      { label: 'Reference related issue', keys: ['M', 'then', 'R'] },
      { label: 'Mark as duplicate of another issue', keys: ['M', 'then', 'M'] },
      { label: 'Link any URL to issue…', keys: ['Ctrl', 'L'] },
      { label: 'Copy issue id', keys: ['⌘', '.'] },
      { label: 'Copy git branch name', keys: ['⌘', '⇧', '.'] },
      { label: 'Copy issue URL', keys: ['⌘', '⇧', ','] },
      { label: 'Copy issue title', keys: ['⌘', '⇧', "'"] },
      { label: 'Copy everything as Markdown', keys: ['⌘', '⌥', 'C'] },
      { label: 'Copy as prompt', keys: ['⌘', '⌥', 'P'] },
      { label: 'Create sub-issue', keys: ['⌘', '⇧', 'O'] },
      { label: 'Add to cycle', keys: ['⇧', 'C'] },
      { label: 'Add to project', keys: ['⇧', 'P'] },
    )
    groups.push({ title: 'Inbox', shortcuts: [
      { label: 'Delete notification', keys: ['E'] },
      { label: 'Mark as read/unread', keys: ['U'] },
      { label: 'Mark all as read', keys: ['⌥', 'U'] },
      { label: 'Snooze notification', keys: ['H'] },
    ] })
  }
  return groups
}

export function KeyboardShortcutsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const { sendComments } = useUserPreferences()
  const desktop = useDesktopLayout()
  const [query, setQuery] = useState('')
  const filtered = useMemo(() => shortcutGroups(sendComments === '⌘ Enter' ? ['⌘', 'Enter'] : ['Enter'], desktop).map(group => ({ ...group, shortcuts: group.shortcuts.filter(shortcut =>
    matchesSearch(query, [shortcut.label, shortcut.keys.join(' ')], t),
  ) })).filter(group => group.shortcuts.length), [query, sendComments, t, desktop])

  return <Dialog open={open} onOpenChange={next => { onOpenChange(next); if (!next) setQuery('') }}>
    <DialogContent aria-describedby={undefined} className="sidebar-shortcuts-dialog" closeLabel={t('Close dialog')}>
      <header className="sidebar-shortcuts-header"><DialogTitle>{t('Keyboard Shortcuts')}</DialogTitle></header>
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
