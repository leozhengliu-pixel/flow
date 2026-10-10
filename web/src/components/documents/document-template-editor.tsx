/**
 * Full-page editor for a document template (workspace Settings > Documents,
 * team Settings > Templates): icon, name, optional document title and a rich
 * body that uses the same editor as documents. Saving writes the Markdown,
 * editor JSON and collaborative state the document API expects.
 */
import { Trash2 } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'

import { MentionTextField } from '@/components/editor/mention-text-field'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { createDocumentTemplate, deleteDocumentTemplate, updateDocumentTemplate } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, DocumentTemplate } from '@/types/flow'
import { DocumentIconPicker } from './document-icon'
import './document-template-editor.css'

export interface DocumentTemplateEditorProps {
  data: BootstrapData
  /** Empty for a workspace template. */
  teamId: string
  /** null creates a new template. */
  template: DocumentTemplate | null
  onClose: () => void
  /** Called after a save or delete so the caller reloads workspace data. */
  onSaved: () => Promise<void>
}

export function DocumentTemplateEditor({ data, teamId, template, onClose, onSaved }: DocumentTemplateEditorProps) {
  const { t } = useI18n()
  const [name, setName] = useState(template?.name ?? '')
  const [description, setDescription] = useState(template?.description ?? '')
  const [title, setTitle] = useState(template?.title ?? '')
  const [icon, setIcon] = useState(template?.icon ?? '')
  const [content, setContent] = useState(template?.content ?? '')
  const snapshot = useRef<DescriptionSnapshot | undefined>(undefined)
  const [saving, setSaving] = useState(false)
  const initialState = useMemo(() => template?.contentData ? JSON.stringify(template.contentData) : template?.contentState, [template])
  const save = async () => {
    if (!name.trim() || saving) return
    setSaving(true)
    try {
      const input = {
        teamId, name: name.trim(), description, title, icon, content,
        // Only a touched body replaces the stored editor state.
        ...(snapshot.current ? { contentState: snapshot.current.contentState, contentData: snapshot.current.document as Record<string, unknown> } : {}),
      }
      if (template) await updateDocumentTemplate(template.id, input)
      else await createDocumentTemplate(input)
      await onSaved()
      toast.success(t('Template saved'))
      onClose()
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not save template'))
    } finally {
      setSaving(false)
    }
  }
  const remove = async () => {
    if (!template) return
    try {
      await deleteDocumentTemplate(template.id)
      await onSaved()
      onClose()
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not delete template'))
    }
  }
  return <div className="document-template-editor">
    <header>
      <button type="button" onClick={onClose}>{t('Cancel')}</button>
      <strong>{t(template ? 'Edit document template' : 'New document template')}</strong>
      <button className="primary" type="button" disabled={!name.trim() || saving} onClick={() => void save()}>{t(saving ? 'Saving…' : 'Save')}</button>
    </header>
    <div className="document-template-editor__body">
      <div className="document-template-editor__name">
        <DocumentIconPicker document={{ icon, color: '' }} ariaLabel={t('Template icon')} triggerClassName="document-template-editor__icon" onChange={visual => setIcon(visual.icon)}/>
        <input autoFocus aria-label={t('Template name')} placeholder={t('Template name')} value={name} onChange={event => setName(event.target.value)}/>
      </div>
      <label>{t('Template description')}<input value={description} onChange={event => setDescription(event.target.value)}/></label>
      <label>{t('Document title')}<input placeholder={t('New document')} value={title} onChange={event => setTitle(event.target.value)}/></label>
      <div className="document-template-editor__content">
        <span>{t('Document content')}</span>
        <MentionTextField
          className="document-template-editor__editor"
          ariaLabel={t('Document content')}
          placeholder={t('Start writing…')}
          value={content}
          state={initialState}
          users={data.users}
          onChange={(markdown, next) => { setContent(markdown); snapshot.current = next }}
          onSubmit={() => void save()}
        />
      </div>
      {template && <button className="document-template-editor__delete" type="button" onClick={() => void remove()}><Trash2 size={14}/>{t('Delete template')}</button>}
    </div>
  </div>
}
