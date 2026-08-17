import { File, FileImage, LoaderCircle, Paperclip, RotateCcw, Trash2 } from 'lucide-react'
import type { Attachment } from '@/types/flow'

export type AttachmentUploadState = { name: string; progress: number; error?: string; file?: File }
export function IssueAttachments({ attachments, upload, onRetry, onDelete }: { attachments: Attachment[]; upload?: AttachmentUploadState; onRetry: (file: File) => void; onDelete: (id: string) => void }) {
  if (!attachments.length && !upload) return null
  return <section className="issue-attachments"><header><strong>Attachments</strong><span>{attachments.length}</span></header><div className="attachment-grid">
    {attachments.map(attachment => { const image=attachment.contentType.startsWith('image/'); return <article className="attachment-card" key={attachment.id}>{image ? <a className="attachment-preview" href={attachment.url} target="_blank" rel="noreferrer"><img src={attachment.url} alt={attachment.title}/></a> : <a className="attachment-file" href={attachment.url} target="_blank" rel="noreferrer"><File size={22}/></a>}<div><a href={attachment.url} target="_blank" rel="noreferrer">{attachment.title}</a><small>{formatBytes(attachment.size)}</small></div><button type="button" aria-label={`Delete attachment ${attachment.title}`} onClick={() => onDelete(attachment.id)}><Trash2 size={13}/></button></article> })}
    {upload && <article className={`attachment-card upload${upload.error ? ' error' : ''}`}><span className="attachment-file">{upload.error ? <FileImage size={22}/> : <LoaderCircle className="spin" size={22}/>}</span><div><strong>{upload.name}</strong><small>{upload.error ?? 'Uploading…'}</small><i style={{width:`${upload.progress}%`}}/></div>{upload.error && upload.file && <button type="button" aria-label="Retry upload" onClick={() => onRetry(upload.file!)}><RotateCcw size={13}/></button>}</article>}
  </div></section>
}
export function AttachmentButton({onClick}:{onClick:()=>void}){return <button type="button" aria-label="Attach images, files, or videos" onClick={onClick}><Paperclip size={14}/></button>}
function formatBytes(size:number){if(size<1024)return `${size} B`;if(size<1024*1024)return `${Math.round(size/1024)} KB`;return `${(size/1024/1024).toFixed(1)} MB`}
