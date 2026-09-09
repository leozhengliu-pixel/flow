import type { BootstrapData,Issue,IssueUpdateInput } from '@/types/flow'

export function configuredIssueBranch(issue:Issue,data?:BootstrapData) {
  const person=issue.assignee??issue.creator
  const username=(person.userId&&person.userId!==person.id?person.userId:person.name&&person.name!==person.id?person.name:person.email?.split('@')[0]||'user').normalize('NFC').toLowerCase().replace(/[^\p{L}\p{N}_-]+/gu,'-')
  const title=issue.title.normalize('NFC').toLowerCase().replace(/[^\p{L}\p{N}_-]+/gu,'-').replace(/^-+|-+$/g,'')||'issue'
  const identifier=issue.identifier.toLowerCase()
  const integration=data?.integrationConnections?.find(item=>['github','gitlab'].includes(item.provider)&&item.config?.branchFormat)
  const format=integration?.config?.branchFormat??data?.userSettings?.[data.viewer.id]?.branchFormat??'username/identifier-title'
  const templates:Record<string,string>={'username/identifier-title':'{username}/{identifier}-{title}','identifier-title':'{identifier}-{title}','identifier/title':'{identifier}/{title}'}
  return (templates[format]??format).replace(/\{(username|identifier|title)\}/g,(_,key)=>({username,identifier,title})[key as 'username'|'identifier'|'title']).replace(/[^\p{L}\p{N}_/-]+/gu,'-').replace(/\/+/g,'/').replace(/^[-/]+|[-/]+$/g,'').slice(0,160)
}

export async function copyIssueForWork(text:string,kind:'branch'|'prompt',issue:Issue,data:BootstrapData|undefined,onUpdate:(input:IssueUpdateInput)=>Promise<void>) {
  await navigator.clipboard.writeText(text)
  const preferences=data?.userSettings?.[data.viewer.id]
  if (!(kind==='branch'?preferences?.gitBranchMoveStarted:preferences?.codingToolMoveStarted)||!data||['started','completed','canceled'].includes(issue.state.type))return
  const scoped=data.states.some(state=>state.teamId===issue.team.id)
  const started=data.states.filter(state=>state.type==='started'&&(scoped?state.teamId===issue.team.id:!state.teamId)).sort((a,b)=>(a.position??0)-(b.position??0))[0]
  if(started) {try{await onUpdate({stateId:started.id})}catch{throw new Error('Copied, but the issue status could not be updated')}}
}
