import { ChevronRight, FileText, Link2, Paperclip, Plus, SmilePlus, UserRound, X } from 'lucide-react'
import type { ActivityEvent, BootstrapData, Comment, Issue, IssueRelationType, IssueUpdateInput, Presence } from '@/types/flow'
import { Button } from '@/components/ui/button'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { ActivityTimeline } from '@/components/activity/activity-timeline'
import { Composer } from '@/components/editor/composer'
import { PropertyMenu } from '@/components/property/property-menu'
import { Avatar } from '@/components/issue/issue-row'
import { StatusIcon } from '@/components/issue/issue-icons'
import { SubIssueEditor } from '@/components/issue/sub-issue-editor'
import { RelationPicker } from '@/components/issue/relation-picker'
import { IssueHeader } from '@/components/issue/issue-header'
import { IssueTitleEditor } from '@/components/issue/issue-title-editor'
import { AssigneePicker, PriorityPicker, StatusPicker } from '@/components/issue/core-property-pickers'
import { LabelPicker, ProjectPicker } from '@/components/issue/label-project-pickers'
import { IssueAttachments, type AttachmentUploadState } from '@/components/issue/issue-attachments'
import { DueDatePicker } from '@/components/issue/due-date-picker'
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useEffect, useRef, useState } from 'react'
import { useIssueAutosave } from '@/components/issue/editor/use-issue-autosave'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import type { SubIssueInput } from '@/components/issue/sub-issue-editor'
import { IssueSLAIndicator } from '@/components/issue/issue-sla-indicator'
import type { IssueOptionsActions } from '@/components/issue/issue-options-menu'
import { documentPath } from '@/lib/app-routes'
import { labelsForResource } from '@/lib/labels'

export function DetailPane({issue,data,comments,activities,presence=[],full=false,embedded=false,issueOptionsActions,onClose,onNavigateIssue,onUpdate,onDelete,onCreateSubIssue,onReactIssue,onComment,onEditComment,onDeleteComment,onReactComment,onRelation,onDeleteRelation,onUpload,onDeleteAttachment}:{issue:Issue;data:BootstrapData;comments:Comment[];activities:ActivityEvent[];presence?:Presence[];full?:boolean;embedded?:boolean;issueOptionsActions?:IssueOptionsActions;onClose:()=>void;onExpand?:()=>void;onNavigateIssue?:(issue:Issue)=>void;onUpdate:(input:IssueUpdateInput)=>Promise<void>;onDelete:()=>Promise<void>;onCreateSubIssue:(input:SubIssueInput)=>Promise<void>;onReactIssue:(emoji:string)=>Promise<void>;onComment:(body:string,bodyData?:Record<string,unknown>,parentId?:string)=>Promise<void>;onEditComment:(id:string,body:string,bodyData?:Record<string,unknown>)=>Promise<void>;onDeleteComment:(id:string)=>Promise<void>;onReactComment:(id:string,emoji:string)=>Promise<void>;onRelation:(type:IssueRelationType,relatedIssueId:string)=>Promise<void>;onDeleteRelation:(relationId:string)=>Promise<void>;onUpload:(file:File)=>Promise<void>;onDeleteAttachment:(attachmentId:string)=>Promise<void>}){
  const[title,setTitle]=useState(issue.title),[description,setDescription]=useState(issue.description),[subOpen,setSubOpen]=useState(false),[relationType,setRelationType]=useState<IssueRelationType|null>(null),[uploadState,setUploadState]=useState<AttachmentUploadState>()
  const fileRef=useRef<HTMLInputElement>(null)
  const {state:saveState,schedule,flush,retry}=useIssueAutosave(onUpdate)
  useEffect(()=>{setTitle(issue.title);setDescription(issue.description)},[issue.id,issue.title,issue.description])
  const subIssues=issue.subIssueIds.map(id=>data.issues.find(i=>i.id===id)).filter(Boolean) as Issue[]
  const sequence=data.issues.filter(item=>!item.archivedAt)
  const sequenceIndex=Math.max(0,sequence.findIndex(item=>item.id===issue.id))
  const navigateIssue=(direction:'next'|'previous')=>{const next=sequence[sequenceIndex+(direction==='next'?1:-1)];if(next)onNavigateIssue?.(next)}
  const related=(id:string)=>data.issues.find(i=>i.id===id)
  const availableStates=statesForIssue(data,issue)
  const availableLabels=labelsForResource(data.labels,'issue').filter(label=>!label.scope||label.scope==='Workspace'||label.scope===issue.team.id)
  const linkedDocuments=data.documents.filter(document=>document.issueId===issue.id&&!document.archivedAt)
  const customerRequests=data.customerRequests.filter(request=>request.issueId===issue.id)
  const toggleLabel=async(id:string)=>{const ids=issue.labels.map(x=>x.id);await onUpdate({labelIds:ids.includes(id)?ids.filter(x=>x!==id):[...ids,id]})}
  const toggleSubscriber=async(id:string)=>onUpdate({subscriberIds:issue.subscriberIds.includes(id)?issue.subscriberIds.filter(x=>x!==id):[...issue.subscriberIds,id]})
  const upload=async(file:File)=>{setUploadState({name:file.name,progress:20,file});try{setUploadState({name:file.name,progress:70,file});await onUpload(file);setUploadState(undefined)}catch(error){setUploadState({name:file.name,progress:100,file,error:error instanceof Error?error.message:'Upload failed'})}}
  return <section className={`issue-view ${full?'full':''} ${embedded?'issue-view--embedded':''}`}>
    {!embedded&&<IssueHeader
      issue={issue} states={availableStates} presence={presence} saveState={saveState} data={data} activities={activities} issueOptionsActions={issueOptionsActions}
      onRetrySave={()=>void retry()} position={sequenceIndex+1} total={sequence.length}
      onClose={()=>{void flush().then(saved=>{if(saved)onClose()})}}
      onNavigate={direction=>{void flush().then(saved=>{if(saved)navigateIssue(direction)})}}
      onUpdate={onUpdate} onDelete={onDelete} onRelation={setRelationType}
    />}
    <div className="issue-scroll"><div className="issue-layout">
      <article className="issue-document">
        <IssueTitleEditor value={title} onChange={value=>{setTitle(value);schedule({title:value})}} onBlur={()=>void flush()}/>
        <div className="issue-mobile-properties" aria-label="Issue properties">
          <StatusPicker value={issue.state} states={availableStates} onChange={stateId=>onUpdate({stateId})}/><PriorityPicker value={issue.priority} onChange={priority=>onUpdate({priority})}/><AssigneePicker value={issue.assignee} users={data.users} onChange={assigneeId=>onUpdate({assigneeId})}/><LabelPicker value={issue.labels} labels={availableLabels} labelGroups={data.labelGroups} onToggle={toggleLabel}/><ProjectPicker value={issue.project} projects={data.projects} teamName={issue.team.name} onChange={projectId=>onUpdate({projectId})}/>
        </div>
        <IssueDescriptionEditor value={description} state={issue.documentContent?.contentData?JSON.stringify(issue.documentContent.contentData):issue.descriptionState} onBlur={()=>void flush()} onChange={snapshot=>{setDescription(snapshot.markdown);schedule({description:snapshot.markdown,descriptionState:snapshot.documentJSON,descriptionData:snapshot.document as Record<string,unknown>,contentState:snapshot.contentState})}}/>
        <div className="document-actions"><EmojiPicker onSelect={onReactIssue}><Button variant="ghost" aria-label="Add reaction"><SmilePlus size={14}/>Add reaction</Button></EmojiPicker><Button variant="ghost" aria-label="Attach images, files, or videos" onClick={()=>fileRef.current?.click()}><Paperclip size={14}/></Button><Button variant="ghost" aria-label="Create new sub-issue" onClick={()=>setSubOpen(true)}><Plus size={14}/>Add sub-issues</Button><input ref={fileRef} type="file" multiple hidden onChange={e=>{for(const file of Array.from(e.target.files??[]))void upload(file);e.target.value='' }}/></div>
        <ReactionPills reactions={issue.reactions} viewerId={data.viewer.id} onToggle={onReactIssue}/>
        {subOpen&&<SubIssueEditor parent={issue} data={data} onCancel={()=>setSubOpen(false)} onCreate={async input=>{await onCreateSubIssue(input);setSubOpen(false)}}/>}
        {subIssues.length>0&&<IssueSection title="Sub-issues" count={subIssues.length}>{subIssues.map(child=><button type="button" className="linked-issue sub-issue-row" key={child.id} onClick={()=>onNavigateIssue?.(child)}><StatusIcon state={child.state}/><span>{child.identifier}</span><strong>{child.title}</strong><em>{child.state.name}</em></button>)}</IssueSection>}
        {issue.relations.length>0&&<IssueSection title="Relations" count={issue.relations.length}>{issue.relations.map(relation=>{const target=related(relation.relatedIssueId);return <div className="linked-issue relation-row" key={relation.id}><Link2 size={14}/><span>{relationLabel(relation.type)}</span><strong>{target?.identifier} {target?.title}</strong><button aria-label="Remove relation" onClick={()=>onDeleteRelation(relation.id)}><X size={12}/></button></div>})}</IssueSection>}
        {linkedDocuments.length>0&&<IssueSection title="Documents" count={linkedDocuments.length}>{linkedDocuments.map(document=><a className="linked-issue issue-resource-row" href={documentPath(data.workspace.urlKey,document)} key={document.id}><FileText size={14}/><span>Document</span><strong>{document.title}</strong></a>)}</IssueSection>}
        {customerRequests.length>0&&<IssueSection title="Customer requests" count={customerRequests.length}>{customerRequests.map(request=>{const customer=data.customers.find(item=>item.id===request.customerId);return <div className="linked-issue issue-resource-row" key={request.id}><UserRound size={14}/><span>{customer?.name??'Customer'}</span><strong>{request.body}</strong></div>})}</IssueSection>}
        <IssueAttachments attachments={issue.attachments} upload={uploadState} onRetry={upload} onDelete={onDeleteAttachment}/>
        <div className="activity-heading"><span>Activity</span><div><Button variant="ghost" size="sm" disabled={Boolean(issue.archivedAt)} onClick={()=>toggleSubscriber(data.viewer.id)}>{issue.subscriberIds.includes(data.viewer.id)?'Unsubscribe':'Subscribe'}</Button><SubscriberMenu issue={issue} users={data.users} onToggle={toggleSubscriber}/></div></div>
        <ActivityTimeline events={activities} comments={comments} viewerId={data.viewer.id} onReply={(body,bodyData,parentId)=>onComment(body,bodyData,parentId)} onEdit={onEditComment} onDelete={onDeleteComment} onReaction={onReactComment} onAttach={()=>fileRef.current?.click()}/><Composer onSubmit={onComment} onAttach={()=>fileRef.current?.click()}/>
      </article>
      <IssueProperties issue={issue} data={data} activities={activities} related={related} onUpdate={onUpdate} onToggleLabel={toggleLabel}/>
    </div></div>
    {relationType&&<RelationPicker open onOpenChange={open=>!open&&setRelationType(null)} type={relationType} issueId={issue.id} issues={data.issues} onSelect={id=>onRelation(relationType,id)}/>}
  </section>
}

function IssueProperties({issue,data,activities,related,onUpdate,onToggleLabel}:{issue:Issue;data:BootstrapData;activities:ActivityEvent[];related:(id:string)=>Issue|undefined;onUpdate:(i:IssueUpdateInput)=>Promise<void>;onToggleLabel:(id:string)=>Promise<void>}){const labels=labelsForResource(data.labels,'issue').filter(label=>!label.scope||label.scope==='Workspace'||label.scope===issue.team.id);const sla=data.issueSlas.find(item=>item.issueId===issue.id&&item.status!=='removed');const rule=sla?data.slaRules.find(item=>item.id===sla.ruleId):undefined;const member=issue.assignee?data.members.find(item=>item.user.id===issue.assignee?.id):undefined;return <aside className="issue-properties"><h3>Properties</h3><StatusPicker value={issue.state} states={statesForIssue(data,issue)} hoverHistory={{activities,issueCreatedAt:issue.createdAt}} onChange={stateId=>onUpdate({stateId})}/><PriorityPicker value={issue.priority} onChange={priority=>onUpdate({priority})}/><div className="issue-assignee-property"><AssigneePicker value={issue.assignee} users={data.users} hoverContext={{member,workspaceName:issue.team.name,project:issue.project}} onChange={assigneeId=>onUpdate({assigneeId})}/>{issue.assignee&&<a className="issue-assignee-profile" href={`/${data.workspace.urlKey}/members`} aria-label="Go to user"><ChevronRight size={15}/></a>}</div>{sla&&<PropertyGroup title="SLA"><IssueSLAIndicator sla={sla} ruleName={rule?.name}/></PropertyGroup>}<PropertyGroup title="Labels"><LabelPicker inline value={issue.labels} labels={labels} labelGroups={data.labelGroups} onToggle={onToggleLabel}/></PropertyGroup><PropertyGroup title="Project"><ProjectPicker value={issue.project} projects={data.projects} teamName={issue.team.name} onChange={projectId=>onUpdate({projectId})}/></PropertyGroup>{issue.parentId&&<PropertyGroup title="Parent"><PropertyMenu compact label="Parent" value={`${related(issue.parentId)?.identifier} ${related(issue.parentId)?.title}`} options={[{id:'',label:'No parent'},...data.issues.filter(i=>i.id!==issue.id).map(i=>({id:i.id,label:`${i.identifier} ${i.title}`}))]} onChange={parentId=>onUpdate({parentId})}/></PropertyGroup>}{issue.dueDate&&<PropertyGroup title="Due date"><DueDatePicker value={issue.dueDate} onChange={dueDate=>onUpdate({dueDate})}/></PropertyGroup>}</aside>}
function PropertyGroup({title,children}:{title:string;children:React.ReactNode}){return <section className="property-group"><h4>{title}</h4>{children}</section>}
function SubscriberMenu({issue,users,onToggle}:{issue:Issue;users:BootstrapData['users'];onToggle:(id:string)=>void}){return <DropdownMenu><DropdownMenuTrigger asChild><button className="subscriber-avatars" aria-label="Change subscribers">{users.filter(u=>issue.subscriberIds.includes(u.id)).slice(0,3).map(u=><Avatar name={u.displayName} key={u.id}/>)}</button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuLabel>Subscribers</DropdownMenuLabel>{users.map(user=><DropdownMenuCheckboxItem checked={issue.subscriberIds.includes(user.id)} key={user.id} onSelect={e=>e.preventDefault()} onCheckedChange={()=>onToggle(user.id)}><Avatar name={user.displayName}/>{user.displayName}</DropdownMenuCheckboxItem>)}</DropdownMenuContent></DropdownMenu>}
function IssueSection({title,count,children}:{title:string;count:number;children:React.ReactNode}){return <section className="issue-detail-section"><header><strong>{title}</strong><span>{count}</span></header>{children}</section>}
function relationLabel(type:IssueRelationType){return type==='blocked_by'?'Blocked by':type==='blocks'?'Blocking':type==='duplicate'?'Duplicate of':type==='parent_of'?'Parent of':type==='sub_issue_of'?'Sub-issue of':'Related'}
function statesForIssue(data:BootstrapData,issue:Issue){const specific=data.states.some(state=>state.teamId===issue.team.id);return data.states.filter(state=>specific?state.teamId===issue.team.id:!state.teamId)}
