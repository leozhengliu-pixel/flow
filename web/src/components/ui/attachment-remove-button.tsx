import { X } from 'lucide-react'

export function AttachmentRemoveButton({ label, onClick }: { label: string; onClick: () => void }) { return <button aria-label={label} className="flow-attachment-remove" onClick={onClick} type="button"><X/></button> }
