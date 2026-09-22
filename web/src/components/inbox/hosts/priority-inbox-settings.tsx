/**
 * LS-0466 PriorityInboxSettingsPage depth — customize which notification types
 * land in the Priority inbox tab (backed by LS-0715 metadata).
 */
import { useMemo, useState } from 'react'

import {
  PRIORITY_INBOX_RULE_GROUPS,
  readPriorityInboxRuleState,
  writePriorityInboxRuleState,
  type PriorityInboxRuleId,
  type PriorityInboxRuleState,
} from './priority-inbox-settings-metadata'

import './inbox-hosts.css'

export type PriorityInboxSettingsProps = {
  /** When false, show enable guidance matching Linear copy. */
  priorityInboxEnabled: boolean
  onEnablePriorityInbox?: () => void
  onChange?: (state: PriorityInboxRuleState) => void
}

export function PriorityInboxSettings({
  priorityInboxEnabled,
  onEnablePriorityInbox,
  onChange,
}: PriorityInboxSettingsProps) {
  const [rules, setRules] = useState<PriorityInboxRuleState>(() => readPriorityInboxRuleState())
  const enabledCount = useMemo(() => Object.values(rules).filter(Boolean).length, [rules])

  const toggle = (id: PriorityInboxRuleId) => {
    setRules(current => {
      const next = { ...current, [id]: !current[id] }
      writePriorityInboxRuleState(next)
      onChange?.(next)
      return next
    })
  }

  return (
    <section className="flow-inbox-priority-settings" data-surface="LS-0466">
      <header>
        <div>
          <h2>Priority inbox</h2>
          <p>Choose which notifications appear in the Priority tab when Priority inbox is enabled.</p>
        </div>
        {!priorityInboxEnabled ? (
          <button className="flow-inbox-host__primary" onClick={onEnablePriorityInbox} type="button">
            Enable Priority inbox
          </button>
        ) : (
          <span className="flow-inbox-priority-settings__count">{enabledCount} active</span>
        )}
      </header>

      {!priorityInboxEnabled ? (
        <div className="flow-inbox-priority-settings__hint" role="status">
          Enable Priority inbox to customize priority types. You can also turn it on from the Inbox display menu.
        </div>
      ) : null}

      <ul className="flow-inbox-priority-settings__rules" data-surface="LS-0715">
        {PRIORITY_INBOX_RULE_GROUPS.map(group => (
          <li key={group.id}>
            <div>
              <strong>{group.title}</strong>
              <p>{group.description}</p>
            </div>
            <button
              aria-checked={rules[group.id]}
              aria-label={`${rules[group.id] ? 'Disable' : 'Enable'} ${group.title}`}
              className={`flow-inbox-priority-settings__toggle${rules[group.id] ? ' is-on' : ''}`}
              disabled={!priorityInboxEnabled}
              onClick={() => toggle(group.id)}
              role="switch"
              type="button"
            >
              <span />
            </button>
          </li>
        ))}
      </ul>
    </section>
  )
}
