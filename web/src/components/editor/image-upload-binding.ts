import type { Editor } from '@tiptap/react'
import type { DescriptionImageUpload } from '@/components/issue/editor/image-extension'

/**
 * Point the editor's image/file extensions at an uploader. The uploader lives in the extension's storage (tiptap's
 * mutable, editor-owned state outside React), so it is bound here rather than by assigning through the hook result.
 */
export function bindImageUpload(editor: Editor, upload: DescriptionImageUpload | undefined) {
  editor.storage.image.upload = upload
}
