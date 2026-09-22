/**
 * LS-0652 WelcomeMessageSettingsPage — dedicated multi-line welcome editor
 * (replaces single-line FieldRow on workspace general settings).
 */
import { useEffect, useState } from 'react'
import './welcome-message-settings.css'

export function WelcomeMessageSettingsPage({
  value,
  onCommit,
}: {
  value: string
  onCommit: (value: string) => void
}) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])

  return (
    <div aria-label="Welcome message" className="welcome-message-settings settings-row">
      <div className="welcome-message-settings__meta">
        <strong>Welcome message</strong>
        <p>Shown to new members when they join this workspace. Keep it short — a greeting, norms, or getting-started links.</p>
      </div>
      <textarea
        aria-label="Welcome message"
        className="settings-input welcome-message-settings__editor"
        maxLength={4000}
        onBlur={() => {
          if (draft !== value) onCommit(draft)
        }}
        onChange={event => setDraft(event.target.value)}
        placeholder="Welcome to the workspace…"
        rows={6}
        value={draft}
      />
    </div>
  )
}

export default WelcomeMessageSettingsPage
