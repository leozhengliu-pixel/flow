import { Link2, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

export function LinkEditor({ initialValue, onApply, onRemove, onClose }: { initialValue: string; onApply: (href: string) => void; onRemove: () => void; onClose: () => void }) {
  const [value, setValue] = useState(initialValue)
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => { inputRef.current?.focus(); inputRef.current?.select() }, [])
  const submit = () => { const href = normalizeHref(value); if (href) onApply(href) }
  return <div className="description-link-editor" onMouseDown={event => event.stopPropagation()}>
    <Link2 size={14}/>
    <input ref={inputRef} aria-label="Link URL" value={value} placeholder="Paste or type a link…" onChange={event => setValue(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); submit() } if (event.key === 'Escape') { event.preventDefault(); onClose() } }}/>
    {initialValue && <button type="button" aria-label="Remove link" onMouseDown={event => event.preventDefault()} onClick={onRemove}><Trash2 size={14}/></button>}
  </div>
}

function normalizeHref(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return ''
  if (/^(https?:\/\/|mailto:|tel:|\/|#)/i.test(trimmed)) return trimmed
  return `https://${trimmed}`
}
