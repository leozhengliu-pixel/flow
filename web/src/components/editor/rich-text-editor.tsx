import { EditorContent, useEditor } from '@tiptap/react'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { useEffect } from 'react'

export function RichTextEditor({value,state,onChange,placeholder}:{value:string;state?:string;onChange?:(value:string,state?:string)=>void;placeholder?:string}){
  const editor=useEditor({
    immediatelyRender:false,
    extensions:[StarterKit.configure({heading:{levels:[2,3]}}),Placeholder.configure({placeholder:placeholder||''}),Markdown],
    content:initialContent(value,state),
    contentType:!state?'markdown':'json',
    editorProps:{
      attributes:{class:'flow-prosemirror description-editor','aria-label':'Issue description'},
    },
    onUpdate:({editor})=>onChange?.(editor.getMarkdown(),JSON.stringify(editor.getJSON())),
  })
  useEffect(()=>{if(!editor)return;const next=initialContent(value,state);const nextString=typeof next==='string'?next:JSON.stringify(next);const currentString=JSON.stringify(editor.getJSON());if(currentString!==nextString)editor.commands.setContent(next,{emitUpdate:false,contentType:!state?'markdown':'json'})},[editor,state,value])
  return <EditorContent editor={editor}/>
}

function initialContent(value:string,state?:string){
  if(state){try{return JSON.parse(state)}catch{}}
  if(!value.trim())return {type:'doc',content:[{type:'paragraph'}]}
  return value
}
