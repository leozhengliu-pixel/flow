/**
 * UpdatesFloatingPanel — floating initiative updates chrome for LS-0311 InitiativePageChrome.
 * Mirrors the list-page InitiativeUpdatesPanel for detail chrome.
 */
import { useState } from 'react'
import { Send, X } from 'lucide-react'
import { Avatar } from '@/components/issue/issue-row'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { SelectControl } from '@/components/ui/select-control'
import type { Initiative, InitiativeMutationInput, InitiativeUpdate, Project, User } from '@/types/flow'
function healthLabel(value: Project['health']) {
  return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No updates' } as const)[value]
}

export type UpdatesFloatingPanelProps = {
  initiative: Initiative
  updates: InitiativeUpdate[]
  viewer: User
  open: boolean
  onClose: () => void
  onOpenActivity: () => void
  onCreateUpdate: (id: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdate: (input: InitiativeMutationInput) => Promise<Initiative>
}

export function UpdatesFloatingPanel({
  initiative,
  updates,
  viewer,
  open,
  onClose,
  onOpenActivity,
  onCreateUpdate,
  onUpdate,
}: UpdatesFloatingPanelProps) {
  const [composing, setComposing] = useState(false)
  const [body, setBody] = useState('')
  const [health, setHealth] = useState<Project['health']>(
    initiative.health === 'noUpdate' ? 'onTrack' : initiative.health,
  )
  const [saving, setSaving] = useState(false)
  if (!open) return null
  const submit = async () => {
    if (!body.trim() || saving) return
    setSaving(true)
    try {
      await onCreateUpdate(initiative.id, { body: body.trim(), health })
      setBody('')
      setComposing(false)
    } finally {
      setSaving(false)
    }
  }
  return (
    <div
      className="li-update-panel-backdrop"
      data-updates-floating-panel
      onMouseDown={event => {
        if (event.currentTarget === event.target) onClose()
      }}
    >
      <aside className="li-update-panel" aria-label="Initiative updates">
        <header>
          <button data-i18n-ignore onClick={onOpenActivity} type="button">
            <ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'} />
            <span>{initiative.name}</span>
          </button>
          <button
            aria-pressed={initiative.subscribed}
            onClick={() => void onUpdate({ subscribed: !initiative.subscribed })}
            type="button"
          >
            {initiative.subscribed ? 'Subscribed' : 'Subscribe'}
          </button>
          <button onClick={() => setComposing(true)} type="button">
            New update
          </button>
          <button aria-label="Close initiative updates" onClick={onClose} type="button">
            <X size={15} />
          </button>
        </header>
        {composing && (
          <div className="li-update-panel-composer">
            <div>
              <Avatar name={viewer.displayName || viewer.name} />
              <strong>{viewer.displayName || viewer.name}</strong>
              <SelectControl
                label="Update health"
                value={health}
                onChange={value => setHealth(value as Project['health'])}
                options={[
                  { value: 'onTrack', label: 'On track' },
                  { value: 'atRisk', label: 'At risk' },
                  { value: 'offTrack', label: 'Off track' },
                ]}
              />
            </div>
            <textarea
              autoFocus
              aria-label="Initiative update"
              placeholder="Write an initiative update…"
              value={body}
              onChange={event => setBody(event.target.value)}
            />
            <footer>
              <button onClick={() => setComposing(false)} type="button">
                Cancel
              </button>
              <button disabled={!body.trim() || saving} onClick={() => void submit()} type="button">
                Post update
              </button>
            </footer>
          </div>
        )}
        {!updates.length && !composing ? (
          <div className="li-update-panel-empty">
            <span>
              <Send size={22} />
            </span>
            <strong>Initiative updates</strong>
            <p>
              Write a short status report to keep everyone up-to-date on the progress and health of
              this initiative
            </p>
            <button onClick={() => setComposing(true)} type="button">
              New initiative update <kbd>N</kbd>
              <span>then</span>
              <kbd>U</kbd>
            </button>
          </div>
        ) : (
          <div className="li-update-panel-list">
            {updates.map(update => (
              <article key={update.id}>
                <header>
                  <Avatar name={update.user.displayName} />
                  <strong data-i18n-ignore>{update.user.displayName}</strong>
                  <time>
                    {new Date(update.createdAt).toLocaleDateString('en', {
                      month: 'short',
                      day: 'numeric',
                    })}
                  </time>
                  <span className={`li-update-health is-${update.health}`} />
                  {healthLabel(update.health)}
                </header>
                <p data-i18n-ignore>{update.body}</p>
              </article>
            ))}
          </div>
        )}
      </aside>
    </div>
  )
}
