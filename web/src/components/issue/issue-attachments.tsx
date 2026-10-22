import { File, FileImage, Link2, Paperclip, RotateCcw, Trash2 } from 'lucide-react'
import type { Attachment } from '@/types/flow'
import { GridLoader } from '@/components/ui/grid-loader'
import { ViaApp } from '@/components/ui/via-app'
import { attributedActorName } from '@/lib/actor-attribution'
import { formatFileSize } from '@/lib/format-file-size'

export type AttachmentUploadState = { name: string; progress: number; error?: string; file?: File }
export function IssueAttachments({ attachments, upload, onRetry, onDelete }: { attachments: Attachment[]; upload?: AttachmentUploadState; onRetry: (file: File) => void; onDelete: (id: string) => void }) {
  if (!attachments.length && !upload) return null
  return <section className="issue-attachments"><header><strong>Attachments</strong><span>{attachments.length}</span></header><div className="attachment-grid">
    {attachments.map(attachment => { const image=attachment.contentType.startsWith('image/'),link=attachment.contentType==='text/uri-list'; return <article className="attachment-card" key={attachment.id}>{image ? <a className="attachment-preview" href={attachment.url} target="_blank" rel="noreferrer"><img src={attachment.url} alt={attachment.title}/></a> : <a className="attachment-file" href={attachment.url} target="_blank" rel="noreferrer">{link?(attachment.iconUrl?<img className="attachment-link-icon" src={attachment.iconUrl} alt="" width={22} height={22} referrerPolicy="no-referrer"/>:<Link2 size={22}/>):<File size={22}/>}</a>}<div><a href={attachment.url} target="_blank" rel="noreferrer">{attachment.title}</a><small>{link?(attachment.subtitle||new URL(attachment.url).hostname):formatFileSize(attachment.size)}</small>{attachment.botActor&&<small className="attachment-via"><span data-i18n-ignore>{attributedActorName(attachment.creator,attachment.botActor)}</span><ViaApp user={attachment.creator} botActor={attachment.botActor}/></small>}</div><button type="button" aria-label={`Delete attachment ${attachment.title}`} onClick={() => onDelete(attachment.id)}><Trash2 size={13}/></button></article> })}
    {upload && <article className={`attachment-card upload${upload.error ? ' error' : ''}`}><span className="attachment-file">{upload.error ? <FileImage size={22}/> : <GridLoader size={22}/>}</span><div><strong>{upload.name}</strong><small>{upload.error ?? 'Uploading…'}</small><i style={{width:`${upload.progress}%`}}/></div>{upload.error && upload.file && <button type="button" aria-label="Retry upload" onClick={() => onRetry(upload.file!)}><RotateCcw size={13}/></button>}</article>}
  </div></section>
}
export function AttachmentButton({onClick}:{onClick:()=>void}){return <button type="button" aria-label="Attach images, files, or videos" onClick={onClick}><Paperclip size={14}/></button>}
