import { useEffect, useMemo, useRef } from 'react'
import type { Editor } from '@tiptap/react'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { usePeopleDirectory } from '@/components/property/people-context'
import type { User } from '@/types/flow'
import './mention-text-field.css'

export type MentionTextFieldProps = {
  /** The markdown the field shows. Mentions are stored in it as `@name` (people) and `[label](path)` (every other resource). */
  value: string
  /** Called with the markdown and the whole snapshot (editor JSON as `documentJSON` / `document`) on every edit. */
  onChange: (markdown: string, snapshot: DescriptionSnapshot) => void
  /** The editor JSON saved beside the markdown, when the record keeps one (it keeps mention identity exactly). */
  state?: string
  ariaLabel: string
  placeholder?: string
  className?: string
  /** People the "@" menu offers; defaults to the workspace directory. */
  users?: User[]
  autoFocus?: boolean
  /** Cmd/Ctrl+Enter. */
  onSubmit?: () => void
  onBlur?: () => void
  editorRef?: (editor: Editor | null) => void
}

/**
 * A body / description field with Flow's mentions: the same rich editor as an issue description (the "@" menu with every
 * resource kind, pasted Flow URLs and typed identifiers becoming chips, hover cards, in-app links), sized like a form
 * field. It replaces a `<textarea>` that holds markdown: `value` / `onChange` carry the same markdown string, so records
 * and the API stay as they are. Clearing `value` (after a submit) empties the editor even while it has focus.
 */
export function MentionTextField({ value, onChange, state, ariaLabel, placeholder, className, users, autoFocus, onSubmit, onBlur, editorRef }: MentionTextFieldProps) {
  const directory = usePeopleDirectory()
  const people = useMemo(() => users ?? [...directory.users.values()], [directory.users, users])
  const editor = useRef<Editor | null>(null)
  const focused = useRef(false)
  const attach = (next: Editor | null) => {
    editor.current = next
    editorRef?.(next)
    if (next && autoFocus && !focused.current) {
      focused.current = true
      window.setTimeout(() => { if (!next.isDestroyed) next.commands.focus('end') }, 0)
    }
  }
  useEffect(() => {
    const current = editor.current
    if (value === '' && current && !current.isDestroyed && !current.isEmpty) current.commands.clearContent(false)
  }, [value])
  return <div className={['mention-text-field', className].filter(Boolean).join(' ')}>
    <IssueDescriptionEditor
      ariaLabel={ariaLabel}
      editorRef={attach}
      onBlur={onBlur}
      onChange={snapshot => onChange(snapshot.markdown, snapshot)}
      onSubmit={onSubmit}
      placeholder={placeholder}
      state={state}
      users={people}
      value={value}
    />
  </div>
}
