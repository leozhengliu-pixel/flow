import { Check, Copy, RotateCw, ShieldOff } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { ScopedFlowTooltip as FlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { revokeReleasePipelineAccessKey, rotateReleasePipelineAccessKey } from '@/lib/api'
import { SettingsRow } from '@/components/settings/settings-primitives'
import type { ReleasePipeline } from '@/types/flow'

import { Subsection } from './pipeline-settings-subsection'

import { accessKeyState } from './pipeline-settings-model'

async function copyText(text: string) {
  await navigator.clipboard.writeText(text)
}

/**
 * Settings › Releases › pipeline › CI setup › Access key. The secret is shown
 * once after it is generated or rotated; afterwards only its status, age and
 * last use remain, with Rotate / Revoke in a menu.
 */
export function PipelineAccessKeySection({ pipeline, disabled, onChanged }: { pipeline: ReleasePipeline; disabled: boolean; onChanged: () => Promise<void> }) {
  const { locale, t, formatDate, formatRelative } = useI18n()
  // The shared "Active" key reads as in-progress in zh; keys use their own wording.
  const activeLabel = locale === 'zh-CN' ? t('Active access key') : 'Active'
  const [secret, setSecret] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [dialog, setDialog] = useState<'rotate' | 'revoke'>()
  const state = accessKeyState(pipeline)
  const announce = async (key: string, verb: 'created' | 'rotated') => {
    setSecret(key)
    try {
      await copyText(key)
      toast.success(t(verb === 'created' ? 'Access key created and copied to clipboard' : 'Access key rotated and copied to clipboard'), { description: t('You will not be able to see this key again once you navigate away.') })
    } catch {
      toast.success(t(verb === 'created' ? 'Access key created' : 'Access key rotated'), { description: t('You will not be able to see this key again once you navigate away. Please copy it before leaving the page.') })
    }
  }
  const generate = async () => {
    if (busy) return
    setBusy(true)
    try {
      const key = await rotateReleasePipelineAccessKey(pipeline.id)
      await onChanged()
      await announce(key.secret, 'created')
    } catch (error) {
      toast.error(t('Failed to create access key'), { description: error instanceof Error ? error.message : undefined })
    } finally {
      setBusy(false)
    }
  }
  const rotate = async (revokeImmediately: boolean) => {
    setBusy(true)
    try {
      const key = await rotateReleasePipelineAccessKey(pipeline.id, { revokeImmediately })
      await onChanged()
      await announce(key.secret, 'rotated')
    } catch (error) {
      toast.error(t('Failed to rotate access key'), { description: error instanceof Error ? error.message : undefined })
    } finally {
      setBusy(false)
    }
  }
  const revoke = async (immediate: boolean) => {
    setBusy(true)
    try {
      await revokeReleasePipelineAccessKey(pipeline.id, immediate)
      setSecret(undefined)
      await onChanged()
      toast.info(t(immediate ? 'Access key revoked' : 'Access key will be revoked in 1 hour'))
    } catch (error) {
      toast.error(t('Failed to revoke access key'), { description: error instanceof Error ? error.message : undefined })
    } finally {
      setBusy(false)
    }
  }
  const title = t('Access key')
  const description = t('Allows external integrations to interact with this pipeline.')
  if (state === 'none' && !secret) return <Subsection title={title} description={description}>
    <div className="settings-card">
      <SettingsRow title={title} description={t('No access key has been generated yet.')}>
        <button type="button" className="settings-action" disabled={disabled || busy} onClick={() => void generate()}>{t('Generate access key')}</button>
      </SettingsRow>
    </div>
  </Subsection>
  const expiring = state === 'expiring' && pipeline.accessKeyRevokedAt
  const shortDate = (value: string) => formatDate(value, { month: 'short', day: 'numeric', ...(new Date(value).getFullYear() === new Date().getFullYear() ? {} : { year: 'numeric' }) })
  const fullDate = (value: string) => formatDate(value, { dateStyle: 'medium', timeStyle: 'short' })
  return <Subsection title={title} description={description}>
    <div className="settings-card pipeline-settings-key-card">
      {secret && <SettingsRow title={title} description={<span className="pipeline-settings-secret-copy"><code className="pipeline-settings-secret" data-i18n-ignore>{secret}</code><span>{t('This access key will not be visible again. Please copy it now.')}</span></span>}>
        <CopyButton text={secret} label={t('Copy to clipboard')} onCopied={() => toast.info(t('Access key copied to clipboard'))}/>
      </SettingsRow>}
      <div className="settings-row pipeline-settings-key-status">
        <span className="pipeline-settings-key-state"><i data-state={expiring ? 'expiring' : 'active'}/>{expiring ? t('Expiring {date}').replace('{date}', shortDate(expiring)) : activeLabel}</span>
        <span className="pipeline-settings-key-meta">
          {pipeline.accessKeyCreatedAt && <FlowTooltip label={t('Created {time}').replace('{time}', fullDate(pipeline.accessKeyCreatedAt))}><span>{t('Created {time}').replace('{time}', shortDate(pipeline.accessKeyCreatedAt))}</span></FlowTooltip>}
          {pipeline.accessKeyLastUsedAt && <FlowTooltip label={t('Last used {time}').replace('{time}', fullDate(pipeline.accessKeyLastUsedAt))}><span>{t('Last used {time}').replace('{time}', formatRelative(pipeline.accessKeyLastUsedAt))}</span></FlowTooltip>}
        </span>
        {!disabled && <DropdownMenu>
          <DropdownMenuTrigger asChild><button type="button" className="pipeline-settings-icon-button" aria-label={t('Access key actions')} disabled={busy}><LinearGlyph name="ellipsis" size={16}/></button></DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="flow-pipelines-settings-menu">
            <DropdownMenuItem onSelect={() => setDialog('rotate')}><RotateCw/><span>{t('Rotate')}</span></DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setDialog('revoke')}><ShieldOff/><span>{t('Revoke')}</span></DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>}
      </div>
    </div>
    {dialog === 'rotate' && <KeyConfirmDialog
      title={t('Rotate access key?')}
      body={expiring ? t('A new access key will be generated. The current key is already scheduled for revocation at {date} and will continue working until then.').replace('{date}', fullDate(expiring)) : t('A new access key will be generated. The current key will continue working for 1 hour to allow you to update your CI configuration.')}
      option={expiring ? t('Revoke old key now instead of at {date}').replace('{date}', fullDate(expiring)) : t('Revoke old key immediately (no grace period)')}
      confirm={t('Rotate')}
      onClose={() => setDialog(undefined)}
      onConfirm={immediate => void rotate(immediate)}
    />}
    {dialog === 'revoke' && <KeyConfirmDialog
      danger
      title={t(expiring ? 'Revoke access key now?' : 'Revoke access key?')}
      body={t(expiring ? 'The access key will stop working immediately. Any CI pipelines using this key will fail.' : 'The access key will stop working after 1 hour. Any CI pipelines using this key will fail after that time.')}
      option={expiring ? undefined : t('Revoke immediately (no grace period)')}
      confirm={t(expiring ? 'Revoke now' : 'Revoke')}
      onClose={() => setDialog(undefined)}
      onConfirm={immediate => void revoke(Boolean(expiring) || immediate)}
    />}
  </Subsection>
}

function KeyConfirmDialog({ title, body, option, confirm, danger = false, onClose, onConfirm }: { title: string; body: string; option?: string; confirm: string; danger?: boolean; onClose: () => void; onConfirm: (checked: boolean) => void }) {
  const { t } = useI18n()
  const [checked, setChecked] = useState(false)
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent className="flow-pipeline-confirm-dialog" closeLabel={t('Close')} aria-describedby={undefined}>
      <DialogTitle>{title}</DialogTitle>
      <p>{body}</p>
      {option && <label className="flow-pipeline-confirm-check"><input type="checkbox" checked={checked} onChange={event => setChecked(event.target.checked)}/><span>{option}</span></label>}
      <footer>
        <button type="button" className="flow-pipeline-confirm-button" onClick={onClose}>{t('Cancel')}</button>
        <button type="button" className={`flow-pipeline-confirm-button${danger ? ' danger' : ' primary'}`} onClick={() => { onConfirm(checked); onClose() }}>{confirm}</button>
      </footer>
    </DialogContent>
  </Dialog>
}

function CopyButton({ text, label, onCopied }: { text: string; label: string; onCopied?: () => void }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const timer = useRef<number>(undefined)
  useEffect(() => () => window.clearTimeout(timer.current), [])
  const copy = async () => {
    try {
      await copyText(text)
      setCopied(true)
      onCopied?.()
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error(t('Could not copy to clipboard'))
    }
  }
  return <FlowTooltip label={label}><button type="button" className="pipeline-settings-icon-button" aria-label={label} onClick={() => void copy()}>{copied ? <Check/> : <Copy/>}</button></FlowTooltip>
}

/**
 * Path filters (Linear: one glob per line, saved on blur). Editing needs an
 * access key, since only CI syncs use them.
 */
export function PipelinePathFilters({ pipeline, disabled, onSave }: { pipeline: ReleasePipeline; disabled: boolean; onSave: (filters: string[]) => void }) {
  const { t } = useI18n()
  const hasKey = accessKeyState(pipeline) !== 'none'
  const [value, setValue] = useState(() => pipeline.pathFilters.join('\n'))
  const saved = pipeline.pathFilters.join('\n')
  useEffect(() => { setValue(saved) }, [saved])
  const commit = () => {
    const filters = value.split('\n').map(line => line.trim()).filter(Boolean)
    if (filters.join('\n') !== saved) onSave(filters)
  }
  return <Subsection title={t('Path filters')} description={t(hasKey ? 'Optionally filter releases to only include commits affecting specific paths. Useful for monorepos where multiple projects share one repository. Supports wildcards, one pattern per line.' : 'Filter releases to only include commits affecting specific paths. Generate an access key to configure path filters.')}>
    <textarea className="pipeline-settings-textarea" aria-label={t('Path filters')} disabled={disabled || !hasKey} value={value} rows={Math.max(2, value.split('\n').length)} spellCheck={false} onChange={event => setValue(event.target.value)} onBlur={commit} placeholder={'frontend/**\npackages/api/**'}/>
  </Subsection>
}
