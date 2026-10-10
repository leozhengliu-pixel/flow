/** The "…" menu of a deleted document: Copy ▸ and Restore document. */
import { RotateCcw } from 'lucide-react'

import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { LinearMenuItem, LinearMenuSeparator, LinearSubmenu } from '@/components/ui/row-context-menu'
import type { FlowDocument, TrashEntry } from '@/types/flow'
import {
  copyDocumentMarkdown, copyDocumentTitle, copyDocumentTitleAsLink, copyDocumentUrl, restoreDeletedDocument, type DocumentActionContext,
} from './document-actions'

export function DeletedDocumentMenuItems({ ctx, document, entry }: { ctx: DocumentActionContext; document: FlowDocument; entry: TrashEntry }) {
  return <>
    <LinearSubmenu icon={<IssueActionGlyph label="Copy" fallback={null}/>} label="Copy">
      <LinearMenuItem icon={<IssueActionGlyph label="Copy URL" fallback={null}/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={() => void copyDocumentUrl(ctx, document)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy title" fallback={null}/>} label="Copy title" shortcut="⌘ ⇧ '" onSelect={() => void copyDocumentTitle(ctx, document)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy title as link" fallback={null}/>} label="Copy title as link" shortcut="⌘ C" onSelect={() => void copyDocumentTitleAsLink(ctx, document)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy content as Markdown" fallback={null}/>} label="Copy content as Markdown" shortcut="⌘ ⌥ C" onSelect={() => void copyDocumentMarkdown(ctx, document)}/>
    </LinearSubmenu>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<RotateCcw size={16}/>} label="Restore document" onSelect={() => void restoreDeletedDocument(ctx, entry.id)}/>
  </>
}
