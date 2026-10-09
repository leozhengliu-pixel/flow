import { useState } from 'react'
import { MentionTextField, type MentionTextFieldProps } from '@/components/editor/mention-text-field'

/**
 * A MentionTextField for a form whose value can also change from outside the field (a template or an agent fills the
 * draft, a dialog is reopened with fresher data). The editor reads `value` only when it mounts, so an outside change
 * remounts it with the new markdown. Edits typed in the field never remount it, and clearing the value is left to
 * MentionTextField so focus survives a submit.
 */
export function SyncedMentionField({ onChange, value, ...props }: MentionTextFieldProps) {
  const [emitted, setEmitted] = useState(value)
  const [revision, setRevision] = useState(0)
  if (value !== emitted) {
    setEmitted(value)
    if (value !== '') setRevision(current => current + 1)
  }
  return <MentionTextField {...props} key={revision} onChange={(markdown, snapshot) => { setEmitted(markdown); onChange(markdown, snapshot) }} value={value}/>
}
