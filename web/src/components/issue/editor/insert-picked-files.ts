import { Selection } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'
import { insertEmbedFiles } from './file-extension'
import { insertImageFiles, type DescriptionImageUpload } from './image-extension'

/** Routes picked files to the right block: images become image nodes, everything else a video or file card (all uploaded). */
export function insertPickedFiles(view: EditorView, files: File[], upload?: DescriptionImageUpload) {
  const images = files.filter(file => file.type.startsWith('image/'))
  const rest = files.filter(file => !file.type.startsWith('image/'))
  const insertedImages = images.length ? insertImageFiles(view, images, upload) : false
  // A freshly inserted image leaves itself node-selected; step past it so the file cards don't replace it.
  if (insertedImages && rest.length) view.dispatch(view.state.tr.setSelection(Selection.near(view.state.doc.resolve(view.state.selection.to), 1)))
  const insertedRest = rest.length ? insertEmbedFiles(view, rest, upload) : false
  return insertedImages || insertedRest
}
