/**
 * LS-0600 TeamTriageMemoriesSettingsPage — team triage memory editor (REST via team settings).
 */
import { useMemo, useState } from 'react'
import { Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { updateStructuredTeamSettings } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Team, TeamTriageMemory } from '@/types/flow'
import './team-triage-memories-settings.css'

export type TeamTriageMemoriesSettingsPageProps = {
  data: BootstrapData
  team: Team
  onReload: () => Promise<void>
}

function newId() {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `memory_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

export function TeamTriageMemoriesSettingsPage({
  data,
  team,
  onReload,
}: TeamTriageMemoriesSettingsPageProps) {
  const { t } = useI18n()
  const settings = data.teamSettings?.[team.id]
  const memories = useMemo(
    () => settings?.triageMemories ?? [],
    [settings?.triageMemories],
  )
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)

  const persist = async (next: TeamTriageMemory[]) => {
    setBusy(true)
    try {
      await updateStructuredTeamSettings(team.id, { triageMemories: next })
      await onReload()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save memories'))
    } finally {
      setBusy(false)
    }
  }

  const add = async () => {
    const content = draft.trim()
    if (!content) return
    const now = new Date().toISOString()
    await persist([
      {
        id: newId(),
        content,
        createdAt: now,
        updatedAt: now,
      },
      ...memories,
    ])
    setDraft('')
  }

  const remove = async (id: string) => {
    await persist(memories.filter(item => item.id !== id))
  }

  return (
    <section
      className="flow-team-triage-memories"
      data-team-triage-memories=""
      aria-label={t('Memories')}
    >
      <p className="flow-team-triage-memories__copy">
        {t(
          'Context remembered for this team to improve future triage suggestions. Memories are shared with teammates who can manage triage settings.',
        )}
      </p>

      <label className="flow-team-triage-memories__editor">
        <span>{t('Memory content')}</span>
        <textarea
          rows={4}
          value={draft}
          placeholder={t('Add context the triage agent should remember…')}
          onChange={event => setDraft(event.target.value)}
          disabled={busy}
        />
      </label>
      <div className="flow-team-triage-memories__actions">
        <button type="button" disabled={busy || !draft.trim()} onClick={() => void add()}>
          {t('Add memory')}
        </button>
      </div>

      <ul className="flow-team-triage-memories__list">
        {memories.map(memory => (
          <li key={memory.id}>
            <div className="flow-team-triage-memories__body" data-i18n-ignore>
              {memory.content}
            </div>
            <button
              type="button"
              className="flow-team-triage-memories__delete"
              aria-label={t('Delete memory')}
              disabled={busy}
              onClick={() => void remove(memory.id)}
            >
              <Trash2 size={14} />
            </button>
          </li>
        ))}
      </ul>
      {!memories.length && (
        <div className="flow-team-triage-memories__empty">
          <strong>{t('No memories yet')}</strong>
          <p>{t('Add remembered context to improve future triage suggestions.')}</p>
        </div>
      )}
    </section>
  )
}

export default TeamTriageMemoriesSettingsPage
