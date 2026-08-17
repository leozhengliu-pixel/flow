import { MoreHorizontal, Paperclip } from 'lucide-react'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/react'
import { Button } from '@/components/ui/button'
import { useRef, useState } from 'react'
import type { BootstrapData, Issue } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { LabelIcon, NoAssigneeIcon, PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'

export interface SubIssueInput { title:string; description:string; stateId:string; priority:number; assigneeId?:string; labelIds:string[]; attachments:File[] }

export function SubIssueEditor({ parent, data, onCancel, onCreate }: { parent: Issue; data: BootstrapData; onCancel: () => void; onCreate: (input: SubIssueInput) => Promise<void> }) {
  const [title, setTitle] = useState(''), [description, setDescription] = useState(''), [saving, setSaving] = useState(false)
  const [stateId,setStateId]=useState(parent.state.id),[priority,setPriority]=useState(parent.priority),[assigneeId,setAssigneeId]=useState(parent.assignee?.id ?? ''),[labelIds,setLabelIds]=useState(parent.labels.map(label=>label.id)),[attachments,setAttachments]=useState<File[]>([])
  const fileRef=useRef<HTMLInputElement>(null)
  const submit = async () => { if (!title.trim() || saving) return; setSaving(true); try { await onCreate({title:title.trim(),description:description.trim(),stateId,priority,assigneeId:assigneeId||undefined,labelIds,attachments}) } finally { setSaving(false) } }
  const titleEditor = useEditor({ immediatelyRender:false, extensions:[StarterKit.configure({heading:false,bulletList:false,orderedList:false,blockquote:false,codeBlock:false,horizontalRule:false}),Placeholder.configure({placeholder:'Issue title'})], content:{type:'doc',content:[{type:'paragraph'}]}, autofocus:true, editorProps:{attributes:{class:'sub-issue-title-editor','aria-label':'Issue title'},handleKeyDown:(_view,event)=>{if(event.key==='Enter'){event.preventDefault();descriptionEditor?.commands.focus('start');return true}if(event.key==='Escape'){event.preventDefault();onCancel();return true}return false}}, onUpdate:({editor})=>setTitle(editor.getText()) })
  const descriptionEditor=useEditor({ immediatelyRender:false, extensions:[StarterKit.configure({heading:{levels:[2,3]}}),Placeholder.configure({placeholder:'Add description…'})], content:{type:'doc',content:[{type:'paragraph'}]}, editorProps:{attributes:{class:'sub-issue-description-editor','aria-label':'Issue description'},handleKeyDown:(_view,event)=>{if((event.metaKey||event.ctrlKey)&&event.key==='Enter'){event.preventDefault();void submit();return true}if(event.key==='Escape'){event.preventDefault();titleEditor?.commands.focus('end');return true}return false}}, onUpdate:({editor})=>setDescription(editor.getText({blockSeparator:'\n'})) })
  const teamStates=data.states.filter(state=>data.states.some(item=>item.teamId===parent.team.id)?state.teamId===parent.team.id:!state.teamId)
  const state=teamStates.find(item=>item.id===stateId)??parent.state, assignee=data.users.find(user=>user.id===assigneeId)
  const labels=data.labels.filter(label=>!label.scope||label.scope==='Workspace'||label.scope===parent.team.id)
  const toggleLabel=(id:string)=>setLabelIds(current=>current.includes(id)?current.filter(value=>value!==id):[...current,id])
  return <form className="sub-issue-editor" onSubmit={event=>{event.preventDefault();void submit()}}>
    <div className="sub-issue-editor-body"><EditorContent editor={titleEditor}/><EditorContent editor={descriptionEditor}/></div>
    <div className="sub-issue-actions"><div className="sub-issue-properties">
      <PropertyMenu compact label="Status" value={state.name} selectedId={stateId} icon={<StatusIcon state={state}/>} options={teamStates.map(item=>({id:item.id,label:item.name,icon:<StatusIcon state={item}/>}))} onChange={setStateId}/>
      <PropertyMenu compact label="Priority" value={priority?['','Urgent','High','Medium','Low'][priority]:'Priority'} selectedId={String(priority)} icon={<PriorityIcon priority={priority}/>} options={['No priority','Urgent','High','Medium','Low'].map((label,id)=>({id:String(id),label,icon:<PriorityIcon priority={id}/>}))} onChange={id=>setPriority(Number(id))}/>
      <PropertyMenu compact label="Assignee" value={assignee?.displayName??'Assignee'} selectedId={assigneeId} icon={assignee?<Avatar name={assignee.displayName}/>:<NoAssigneeIcon size={14}/>} options={[{id:'',label:'No assignee',icon:<NoAssigneeIcon size={14}/>},...data.users.map(user=>({id:user.id,label:user.displayName,icon:<Avatar name={user.displayName}/>}))]} onChange={setAssigneeId}/>
      <PropertyMenu compact multiple label="Labels" value={labelIds.length?`${labelIds.length} labels`:'Labels'} selectedIds={labelIds} icon={<LabelIcon size={14}/>} options={labels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope }))} onChange={toggleLabel}/>
      <button type="button" aria-label="More actions"><MoreHorizontal size={14}/></button>
      <button type="button" aria-label="Attach images, files, or videos" title={attachments.length?`${attachments.length} file${attachments.length===1?'':'s'} selected`:undefined} onClick={()=>fileRef.current?.click()}><Paperclip size={14}/>{attachments.length>0&&<span>{attachments.length}</span>}</button><input ref={fileRef} type="file" multiple hidden onChange={event=>{setAttachments(Array.from(event.target.files??[]));event.target.value=''}}/>
    </div><Button type="button" variant="ghost" aria-label="Discard sub-issue" onClick={onCancel}>Cancel</Button><Button type="submit" disabled={!title.trim()||saving}>{saving?'Creating…':'Create'}</Button></div>
  </form>
}
