/**
 * Full-page editor for a document template (workspace Settings › Documents, team Settings › Templates), laid out
 *: a back link, the "New document template" heading over a divider, the page icon, the template name
 * and a rich body that uses the same editor as documents. Saving writes the Markdown, editor JSON and collaborative
 * state the document API expects.
 */
import { useMemo, useRef, useState } from 'react'
import { toast } from '@/lib/toast'

import { MentionTextField } from '@/components/editor/mention-text-field'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { confirmTemplateAction } from '@/components/settings/template-confirm'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { TemplateActionsMenu, TemplateListRow } from '@/components/settings/issue-template-settings'
import { DocumentGlyph } from './document-icon'
import { createDocumentTemplate, deleteDocumentTemplate, updateDocumentTemplate } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, DocumentTemplate } from '@/types/flow'
import { DocumentIconPicker } from './document-icon'
import '@/components/settings/issue-template-editor.css'
import './document-template-editor.css'

/** The reference app draws a template's page icon in this neutral grey. */
const DOCUMENT_TEMPLATE_ICON_COLOR = '#bec2c8'

export interface DocumentTemplateEditorProps {
  data: BootstrapData
  /** Empty for a workspace template. */
  teamId: string
  /** null creates a new template. */
  template: DocumentTemplate | null
  /** The back link's label: "Documents" (workspace) or "Team templates". */
  backLabel?: string
  onClose: () => void
  /** Called after a save or delete so the caller reloads workspace data. */
  onSaved: () => Promise<void>
}

export function DocumentTemplateEditor({ data, teamId, template, backLabel = 'Documents', onClose, onSaved }: DocumentTemplateEditorProps) {
  const { t } = useI18n()
  const [name, setName] = useState(template?.name ?? '')
  const [icon, setIcon] = useState(template?.icon ?? '')
  const [color, setColor] = useState(template?.color ?? '')
  const [content, setContent] = useState(template?.content ?? '')
  const snapshot = useRef<DescriptionSnapshot | undefined>(undefined)
  const [saving, setSaving] = useState(false)
  const initialState = useMemo(() => template?.contentData ? JSON.stringify(template.contentData) : template?.contentState, [template])
  const dirty = name !== (template?.name ?? '') || icon !== (template?.icon ?? '') || color !== (template?.color ?? '') || content !== (template?.content ?? '')
  const close = async () => {
    if (dirty && !(await confirmTemplateAction({ title: t('Discard changes?'), description: t('Confirm that you want to discard your changes.'), confirmLabel: t('Discard'), cancelLabel: t('Cancel') }))) return
    onClose()
  }
  const nameRef = useRef<HTMLInputElement>(null)
  const save = async () => {
    if (saving) return
    if (!name.trim()) { nameRef.current?.focus(); return }
    setSaving(true)
    try {
      const input = {
        teamId, name: name.trim(), icon, color, content,
        // Only a touched body replaces the stored editor state.
        ...(snapshot.current ? { contentState: snapshot.current.contentState, contentData: snapshot.current.document as Record<string, unknown> } : {}),
      }
      if (template) await updateDocumentTemplate(template.id, input)
      else await createDocumentTemplate({ ...input, description: '', title: '' })
      await onSaved()
      onClose()
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not save template'))
    } finally {
      setSaving(false)
    }
  }
  const remove = async () => {
    if (!template) return
    if (!(await confirmTemplateAction({ title: t('Delete the template "{name}"?').replace('{name}', template.name), description: t('You cannot undo this action.'), confirmLabel: t('Delete'), cancelLabel: t('Cancel') }))) return
    try {
      await deleteDocumentTemplate(template.id)
      await onSaved()
      onClose()
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not delete template'))
    }
  }
  return <div className="tpl-editor tpl-doc-editor" data-i18n-ignore>
    <button type="button" className="tpl-back-link" onClick={() => void close()}>
      <FlowGlyph name="chevronLeft" size={14}/>
      <span>{t(backLabel)}</span>
    </button>
    <div className="tpl-doc-column">
      <header className="tpl-doc-header">
        <h1>{t(template ? 'Edit document template' : 'New document template')}</h1>
        {template && <TemplateActionsMenu className="tpl-editor-menu" items={[{ label: 'Delete', icon: <FlowGlyph name="delete" size={16}/>, onSelect: () => void remove() }]}/>}
      </header>
      <span className="tpl-doc-divider" aria-hidden="true"/>
      <div className="tpl-doc-body">
        <span className="tpl-doc-icon">
          <DocumentIconPicker document={{ icon, color: color || DOCUMENT_TEMPLATE_ICON_COLOR }} ariaLabel={t('Document icon')} triggerClassName="tpl-doc-icon-trigger" onChange={visual => { setIcon(visual.icon); setColor(visual.color) }}/>
        </span>
        <input ref={nameRef} autoFocus className="tpl-doc-name" aria-label={t('Template name')} placeholder={t('Template name')} value={name} onChange={event => setName(event.target.value)}/>
        <MentionTextField
          className="tpl-doc-content"
          ariaLabel={t('Document template content')}
          placeholder={t('Click here to start writing…')}
          value={content}
          state={initialState}
          users={data.users}
          onChange={(markdown, next) => { setContent(markdown); snapshot.current = next }}
          onSubmit={() => void save()}
        />
        <footer className="tpl-editor-footer tpl-doc-footer">
          <button type="button" className="tpl-secondary-button" onClick={() => void close()}>{t('Cancel')}</button>
          <button type="button" className="tpl-primary-button" aria-label={t(template ? 'Save' : 'Create')} disabled={saving} onClick={() => void save()}>{t(template ? 'Save' : 'Create')}</button>
        </footer>
      </div>
    </div>
  </div>
}

/** A document template row of a settings list: opens the editor, with Edit / Duplicate… / Delete in its hover menu. */
export function DocumentTemplateRow({ template, inheritedFrom, onOpen, onChanged }: { template: DocumentTemplate; inheritedFrom?: string; onOpen: () => void; onChanged: () => Promise<void> }) {
  const { t } = useI18n()
  const duplicate = async () => {
    try {
      await createDocumentTemplate({ teamId: template.teamId, name: t('Copy of {name}').replace('{name}', template.name), description: template.description ?? '', title: template.title ?? '', icon: template.icon ?? '', color: template.color ?? '', content: template.content ?? '', contentState: template.contentState, contentData: template.contentData })
      await onChanged()
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not save template'))
    }
  }
  const remove = async () => {
    if (!(await confirmTemplateAction({ title: t('Delete the template "{name}"?').replace('{name}', template.name), description: t('You cannot undo this action.'), confirmLabel: t('Delete'), cancelLabel: t('Cancel') }))) return
    try {
      await deleteDocumentTemplate(template.id)
      await onChanged()
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not delete template'))
    }
  }
  return <TemplateListRow
    icon={<DocumentGlyph icon={template.icon} color={template.color || DOCUMENT_TEMPLATE_ICON_COLOR}/>}
    color={template.color || DOCUMENT_TEMPLATE_ICON_COLOR}
    name={template.name}
    meta={inheritedFrom ? <>{t('Inherited from')} <b data-i18n-ignore>{inheritedFrom}</b></> : template.description ? <span data-i18n-ignore>{template.description}</span> : template.creator ? <>{t('Created by')} <b data-i18n-ignore>{template.creator.displayName}</b></> : undefined}
    onOpen={() => { if (!inheritedFrom) onOpen() }}
    menu={inheritedFrom ? undefined : <TemplateActionsMenu className="tpl-list-row-menu" items={[
      { label: 'Edit', icon: <FlowGlyph name="edit" size={16}/>, onSelect: onOpen },
      { label: 'Duplicate…', icon: <FlowGlyph name="copy" size={16}/>, onSelect: () => void duplicate() },
      { label: 'Delete', icon: <FlowGlyph name="delete" size={16}/>, onSelect: () => void remove(), separatorBefore: true },
    ]}/>}
  />
}
