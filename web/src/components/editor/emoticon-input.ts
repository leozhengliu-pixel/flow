import type { EditorView } from '@tiptap/pm/view'
import { transformTextEmoticons } from '@/lib/runtime-preferences'

export function handleEmoticonInput(view:EditorView,from:number,to:number,text:string) {
  if (text!==' ' || view.state.selection.$from.parent.type.spec.code || view.state.selection.$from.marks().some(mark=>mark.type.name==='code')) return false
  const before=view.state.doc.textBetween(Math.max(0,from-8),from,' ')
  const match=before.match(/(?:^|\s)(:-?\)|:-?\(|;-?\)|:D|<3)$/)
  if (!match) return false
  const emoticon=match[1],replacement=transformTextEmoticons(emoticon)
  if(replacement===emoticon)return false
  view.dispatch(view.state.tr.insertText(replacement+' ',from-emoticon.length,to))
  return true
}
