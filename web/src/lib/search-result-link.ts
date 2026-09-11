import type { BootstrapData, SearchResult } from '@/types/flow'
import { customerPath, documentPath, initiativePath, issuePath, memberProfilePath, projectPath, projectSavedViewPath, releasePath, releasePipelinesPath, savedViewPathId, teamProjectsSavedViewPath, teamSavedViewPath, projectsSavedViewPath, workspaceSavedViewPath } from './app-routes'

export function searchResultLink(data: BootstrapData, result: SearchResult): string {
  const workspace=data.workspace.urlKey
  if(result.url?.startsWith(`/${encodeURIComponent(workspace)}/`) && !result.url.includes('\\')) {
    const parsed = new URL(result.url, 'https://flow.invalid')
    // Browsers normalize literal and encoded dot segments before navigating.
    if(parsed.origin === 'https://flow.invalid' && parsed.pathname.startsWith(`/${encodeURIComponent(workspace)}/`))return `${parsed.pathname}${parsed.search}${parsed.hash}`
  }
  if(result.type==='issue')return issuePath(workspace,{identifier:result.identifier??result.id,title:result.title})
  if(result.type==='project')return projectPath(workspace,{slugId:result.slugId??data.projects.find(item=>item.id===result.id)?.slugId??result.id})
  if(result.type==='initiative')return initiativePath(workspace,{slugId:result.slugId??data.initiatives.find(item=>item.id===result.id)?.slugId??result.id})
  if(result.type==='document'){
    const document=data.documents.find(item=>item.id===result.id)
    if(!document&&!result.slugId&&result.parentId&&result.parentType==='project')return projectPath(workspace,{slugId:data.projects.find(item=>item.id===result.parentId)?.slugId??result.parentId})
    if(!document&&!result.slugId&&result.parentId&&result.parentType==='initiative')return initiativePath(workspace,{slugId:data.initiatives.find(item=>item.id===result.parentId)?.slugId??result.parentId})
    return documentPath(workspace,{slugId:result.slugId??document?.slugId??result.id})
  }
  if(result.type==='member')return memberProfilePath(workspace,data.users.find(item=>item.id===result.id)?.name??result.id)
  if(result.type==='customer')return customerPath(workspace,{id:result.id,name:result.title})
  if(result.type==='release'){
    const release=data.releases.find(item=>item.id===result.id),pipeline=data.releasePipelines.find(item=>item.id===release?.pipelineId)
    return release&&pipeline?releasePath(workspace,pipeline.slugId,release.slugId):releasePipelinesPath(workspace)
  }
  const view=data.savedViews.find(item=>item.id===result.id)
  if(!view)return workspaceSavedViewPath(workspace,result.id)
  const team=data.teams.find(item=>item.id===view.teamId),project=data.projects.find(item=>item.id===view.projectId),id=savedViewPathId(view)
  if(view.resource==='projects')return team?teamProjectsSavedViewPath(workspace,team.key,id):projectsSavedViewPath(workspace,id)
  return project?projectSavedViewPath(workspace,project.slugId,id):team?teamSavedViewPath(workspace,team.key,id):workspaceSavedViewPath(workspace,id)
}
