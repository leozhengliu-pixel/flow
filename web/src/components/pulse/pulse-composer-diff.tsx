import { useResetOnChange } from '@/hooks/use-reset-on-change'
import { useEffect, useState } from 'react'
import { fetchUpdateDiffPreview } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { Initiative, Project, PulseDiff } from '@/types/flow'
import { PulseDiffBlock } from './pulse-diff-block'

type Source = { kind: 'project'; entity: Project } | { kind: 'initiative'; entity: Initiative }

/**
 * Composer preview of what the new update will record: the server's
 * diff-preview (changes since the previous update's snapshot).
 */
export function PulseComposerDiffPreview({ source }: { source: Source }) {
  const { t } = useI18n()
  const [diff, setDiff] = useState<PulseDiff>()
  const id = source.entity.id
  const kind = source.kind
  useResetOnChange([id, kind], () => setDiff(undefined))
  useEffect(() => {
    const controller = new AbortController()
    fetchUpdateDiffPreview(kind, id, controller.signal)
      .then(result => { if (!controller.signal.aborted) setDiff(result?.diff) })
      .catch(() => undefined)
    return () => controller.abort()
  }, [id, kind])
  return <PulseDiffBlock diff={diff} kind={kind} title={<header className="pulse-diff-title">{t('Changes since last update')}</header>}/>
}
