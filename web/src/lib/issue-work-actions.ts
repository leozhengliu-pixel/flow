import { renderCodingPrompt, type CodingPromptIssue } from '@/lib/coding-tools'
import type { BootstrapData,Issue,IssueUpdateInput,WorkflowState } from '@/types/flow'

export function configuredIssueBranch(issue:Issue,data?:BootstrapData) {
  const person=issue.assignee??issue.creator
  const username=(person.userId&&person.userId!==person.id?person.userId:person.name&&person.name!==person.id?person.name:person.email?.split('@')[0]||'user').normalize('NFC').toLowerCase().replace(/[^\p{L}\p{N}_-]+/gu,'-')
  const title=issue.title.normalize('NFC').toLowerCase().replace(/[^\p{L}\p{N}_-]+/gu,'-').replace(/^-+|-+$/g,'')||'issue'
  const identifier=issue.identifier.toLowerCase()
  const integration=data?.integrationConnections?.find(item=>['github','gitlab'].includes(item.provider)&&item.config?.branchFormat)
  const format=integration?.config?.branchFormat??data?.userSettings?.[data.viewer.id]?.branchFormat??'username/identifier-title'
  const templates:Record<string,string>={'username/identifier-title':'{username}/{identifier}-{title}','username/identifier':'{username}/{identifier}','username-identifier-title':'{username}-{identifier}-{title}','username-identifier':'{username}-{identifier}','identifier-title':'{identifier}-{title}','title-identifier':'{title}-{identifier}','identifier':'{identifier}','feature/identifier-title':'feature/{identifier}-{title}','feature/identifier':'feature/{identifier}','identifier/title':'{identifier}/{title}'}
  return (templates[format]??format).replace(/\{(username|identifier|title)\}/g,(_,key)=>({username,identifier,title})[key as 'username'|'identifier'|'title']).replace(/[^\p{L}\p{N}_/-]+/gu,'-').replace(/\/+/g,'/').replace(/^[-/]+|[-/]+$/g,'').slice(0,160)
}

/** The issue as the coding prompt describes it: the reference app's prompt names its team and its parent issue too. */
export function issuePromptInput(issue:Issue,data:BootstrapData|undefined,url:string):CodingPromptIssue {
  const parent=issue.parentId?data?.issues.find(item=>item.id===issue.parentId):undefined
  return {identifier:issue.identifier,title:issue.title,description:issue.description,branchName:configuredIssueBranch(issue,data),url,team:issue.team?.name,parent:parent?{id:parent.id,identifier:parent.identifier,title:parent.title,description:parent.description}:undefined}
}

/** "Copy as prompt": the viewer's prompt template (the reference app's by default) filled in for the issue. */
export function issueWorkPrompt(issue:Issue,data:BootstrapData|undefined,url:string) {
  return renderCodingPrompt(data?.userSettings?.[data.viewer.id]?.codingPromptTemplate,issuePromptInput(issue,data,url),{omitBranchName:data?.workspaceSettings?.featureFlags?.['suggest-branch-name']===false})
}

/**
 * Copies the branch name or prompt and, when the viewer's preference says so (the reference app: "On git branch copy / On open
 * in coding tool, move issue to started status"), moves an unstarted issue to the team's first started status —
 * unless Option is held (`skipMove`). Resolves with the status it moved the issue to.
 */
export async function copyIssueForWork(text:string,kind:'branch'|'prompt',issue:Issue,data:BootstrapData|undefined,onUpdate:(input:IssueUpdateInput)=>Promise<unknown>,options:{skipMove?:boolean}={}):Promise<WorkflowState|undefined> {
  await navigator.clipboard.writeText(text)
  const preferences=data?.userSettings?.[data.viewer.id]
  if (options.skipMove||!(kind==='branch'?preferences?.gitBranchMoveStarted:preferences?.codingToolMoveStarted)||!data||['started','completed','canceled'].includes(issue.state.type))return undefined
  const scoped=data.states.some(state=>state.teamId===issue.team.id)
  const started=data.states.filter(state=>state.type==='started'&&(scoped?state.teamId===issue.team.id:!state.teamId)).sort((a,b)=>(a.position??0)-(b.position??0))[0]
  if(!started)return undefined
  try{await onUpdate({stateId:started.id})}catch{throw new Error('Copied, but the issue status could not be updated')}
  return started
}
