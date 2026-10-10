import { useState } from 'react'
import { toast } from 'sonner'

import { DocumentGlyph } from '@/components/documents/document-icon'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, ReleaseResource } from '@/types/flow'

import { ReleaseBasicDialog } from './release-page-chrome'

export function ReleaseLinkDialog({ onClose, onSave }: { onClose: () => void; onSave: (resource: ReleaseResource) => Promise<void> }) {
  const { t } = useI18n()
  const [title, setTitle] = useState('')
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = () => {
    if (busy || !url.trim()) return
    setBusy(true)
    const link = url.trim()
    void onSave({ id: `release_resource_${Date.now()}`, type: 'link', title: title.trim() || link, url: link, createdAt: new Date().toISOString() }).catch(error => { setBusy(false); toast.error(error instanceof Error ? error.message : t('Could not save release')) })
  }
  return <ReleaseBasicDialog title={t('Add link')} onClose={onClose}>
    <form className="flow-release-link-form" onSubmit={event => { event.preventDefault(); submit() }}>
      <label>{t('URL')}<input autoFocus type="url" placeholder="https://" value={url} onChange={event => setUrl(event.target.value)}/></label>
      <label>{t('Title')}<input value={title} onChange={event => setTitle(event.target.value)} placeholder={t('Optional')}/></label>
      <footer><button onClick={onClose} type="button">{t('Cancel')}</button><button className="is-primary" disabled={busy || !url.trim()} type="submit">{t('Add link')}</button></footer>
    </form>
  </ReleaseBasicDialog>
}

export function ReleaseDocumentDialog({ data, onClose, onSave }: { data: BootstrapData; onClose: () => void; onSave: (resource: ReleaseResource) => Promise<void> }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState(false)
  const documents = data.documents.filter(item => item.title.toLowerCase().includes(query.trim().toLowerCase())).slice(0, 50)
  return <ReleaseBasicDialog title={t('Add document')} onClose={onClose}>
    <label className="flow-release-document-search">{t('Search')}<input autoFocus value={query} onChange={event => setQuery(event.target.value)} placeholder={t('Search documents…')}/></label>
    <div className="flow-release-document-results">
      {documents.map(document => <button disabled={busy} key={document.id} type="button" onClick={() => { setBusy(true); void onSave({ id: `release_resource_${Date.now()}`, type: 'document', title: document.title, documentId: document.id, createdAt: new Date().toISOString() }).catch(error => { setBusy(false); toast.error(error instanceof Error ? error.message : t('Could not save release')) }) }}><DocumentGlyph document={document}/><span data-i18n-ignore>{document.title}</span></button>)}
      {!documents.length && <p>{t('No documents found')}</p>}
    </div>
  </ReleaseBasicDialog>
}
