import { useEffect, useRef, useState } from 'react'
import { Bot, RefreshCw, Send, Square } from 'lucide-react'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import { canDelegateTo, getApplicationTask, listApplicationTasks, replyApplicationTask, type ApplicationTask, type ApplicationActivity } from '@/lib/application-agents'
import { AgentRichText } from './agent-rich-text'
import { useI18n } from '@/i18n/i18n'
import './application-agents.css'

export function IssueAgentPicker({issue,data,onUpdate}:{issue:Issue;data:BootstrapData;onUpdate:(input:IssueUpdateInput)=>Promise<void>}) {
  const {t}=useI18n()
  const users=data.users.filter(user=>canDelegateTo(user,issue.team.id))
  if(!users.length&&!issue.delegate)return null
  if(data.viewerRole==='guest'&&data.workspaceSettings.preventGuestAgents)return null
  return <div className="core-property-picker"><PersonPicker label="Agent" ariaLabel={t('Delegate to agent')} people={users.map(user=>({...user,label:user.displayName}))} selectedId={issue.delegate?.id} onChange={delegateId=>onUpdate({delegateId})} emptyOptionLabel="No agent" emptyTriggerLabel="Delegate to agent" searchPlaceholder="Find agent…" triggerClassName="core-property-trigger"/></div>
}

export function IssueAgentTasks({issue,data,resourceType='issue'}:{issue:Pick<Issue,'id'|'delegate'|'agentSessionId'>;data:BootstrapData;resourceType?:'issue'|'document'|'project'}) {
  const {t}=useI18n()
  const [tasks,setTasks]=useState<ApplicationTask[]>([]),[revision,setRevision]=useState(0),[error,setError]=useState('')
  const hasApplications = Boolean(issue.delegate || data.users.some(user=>user.app))
  useEffect(()=>{
    if(!hasApplications)return
    const abort=new AbortController();let timer:ReturnType<typeof setTimeout>
    const load=async()=>{let delay=5000;try{const tasks=await listApplicationTasks(data.workspace.urlKey,issue.id,abort.signal,resourceType);if(!abort.signal.aborted){setTasks(tasks);setError('');delay=tasks.some(task=>task.status==='pending'||task.status==='active')?1000:5000}}catch(error){if(!abort.signal.aborted)setError(String(error instanceof Error?error.message:error))}finally{if(!abort.signal.aborted)timer=setTimeout(load,delay)}}
    void load();return()=>{abort.abort();clearTimeout(timer)}
  },[data.workspace.urlKey,issue.id,issue.agentSessionId,revision,hasApplications,resourceType])
  if(!issue.delegate&&!tasks.length&&!error)return null
  return <section className="issue-agent-tasks" aria-label={t('Agent sessions')}><header><Bot size={16}/><strong>{t('Agent sessions')}</strong><button type="button" aria-label={t('Refresh')} onClick={()=>setRevision(value=>value+1)}><RefreshCw size={14}/></button></header>{error&&<p role="alert">{error}</p>}{tasks.map(task=><Task key={task.id} task={task} workspace={data.workspace.urlKey} name={data.users.find(user=>user.id===task.appUserId)?.displayName??t('Application')} onChanged={()=>setRevision(value=>value+1)}/>)}</section>
}

function Task({task,workspace,name,onChanged}:{task:ApplicationTask;workspace:string;name:string;onChanged:()=>void}) {
  const {t}=useI18n()
  const [activities,setActivities]=useState<ApplicationActivity[]>([]),[reply,setReply]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState('')
  const cursor=useRef({key:'',after:''})
  useEffect(()=>{const abort=new AbortController();const key=`${workspace}:${task.id}`;const load=async()=>{const reset=cursor.current.key!==key;let after=reset?'':cursor.current.after;const items:ApplicationActivity[]=[];for(;;){const result=await getApplicationTask(workspace,task.id,after,abort.signal);items.push(...result.activities);if(result.activities.length)after=result.activities.at(-1)!.id;if(result.activities.length<100)break}if(!abort.signal.aborted){cursor.current={key,after};setActivities(current=>reset?items:[...current,...items]);setError('')}};void load().catch(error=>{if(!abort.signal.aborted)setError(String(error.message??error))});return()=>abort.abort()},[workspace,task.id,task.version])
  const submit=async(type:'prompt'|'canceled'|'retry',approve?:boolean)=>{if(busy)return;setBusy(true);try{await replyApplicationTask(workspace,task,type,reply,approve);setReply('');setError('');onChanged()}catch(error){setError(String(error instanceof Error?error.message:error))}finally{setBusy(false)}}
  return <article className="issue-agent-task"><header><span>{name}</span><span className="issue-agent-task-status">{t(({pending:'Pending',active:'Working',awaitingInput:'Needs input',complete:'Completed',error:'Failed',canceled:'Canceled'})[task.status])}</span>{['pending','active','awaitingInput'].includes(task.status)?<button type="button" disabled={busy} aria-label={t('Stop agent')} onClick={()=>void submit('canceled')}><Square size={12}/></button>:<button type="button" disabled={busy} aria-label={t('Retry agent')} onClick={()=>void submit('retry')}><RefreshCw size={12}/></button>}</header>
    <div className="issue-agent-activities">{activities.filter(activity=>activity.type!=='output'||!activities.some(item=>item.type==='response'&&item.id>activity.id)).map(activity=><div key={activity.id} data-type={activity.type}>{activity.type==='thought'?<details><summary>{t('Progress')}</summary><AgentRichText className="issue-agent-markdown" content={activity.body}/></details>:<><small>{t(({response:'Response',output:'Working',action:'Action',elicitation:'Needs input',prompt:'Reply',retry:'Retry',error:'Error',canceled:'Canceled'} as Record<string,string>)[activity.type]??activity.type)}</small><AgentRichText className="issue-agent-markdown" content={activity.body}/>{activity.url&&<a href={activity.url} target="_blank" rel="noreferrer">{t('Open result')}</a>}</>}</div>)}</div>
    {task.status==='awaitingInput'&&(task.pendingTool?<div className="issue-agent-approval"><button type="button" disabled={busy} onClick={()=>void submit('prompt',false)}>{t('Reject')}</button><button type="button" disabled={busy} onClick={()=>void submit('prompt',true)}>{t('Approve')}</button></div>:<form onSubmit={event=>{event.preventDefault();void submit('prompt')}}><textarea aria-label={t('Reply to agent')} value={reply} onChange={event=>setReply(event.target.value)}/><button type="submit" disabled={busy||!reply.trim()} aria-label={t('Send reply')}><Send size={14}/></button></form>)}{error&&<p role="alert">{error}</p>}
  </article>
}
