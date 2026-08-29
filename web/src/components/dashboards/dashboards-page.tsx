import { useEffect, useMemo, useState } from 'react'
import { Bell, BellOff, Download, LayoutDashboard, Link, Plus, Share2, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import type { BootstrapData, Dashboard, DashboardWidget, DashboardWidgetResult } from '@/types/flow'
import { createDashboard, dashboardExportURL, deleteDashboard, fetchDashboardResults, fetchDashboards, shareDashboard, subscribeDashboard, updateDashboard } from '@/lib/api'
import './dashboards-page.css'

const widgetOptions: Array<{type:DashboardWidget['type'];label:string}> = [
  {type:'issue_count',label:'Issue count'}, {type:'status_breakdown',label:'Status breakdown'},
  {type:'assignee_workload',label:'Assignee workload'}, {type:'cycle_progress',label:'Cycle progress'},
  {type:'project_progress',label:'Project progress'}, {type:'sla_health',label:'SLA health'},
  {type:'throughput',label:'Throughput'},
]

export function DashboardsPage({data,onOpenSidebar}:{data:BootstrapData;onOpenSidebar:()=>void}) {
  const [items,setItems]=useState<Dashboard[]>([]),[selectedId,setSelectedId]=useState(''),[results,setResults]=useState<DashboardWidgetResult[]>([]),[busy,setBusy]=useState(false)
  const selected=useMemo(()=>items.find(item=>item.id===selectedId)??items[0],[items,selectedId])
  const load=async()=>{const page=await fetchDashboards();setItems(page.items);setSelectedId(current=>current||page.items[0]?.id||'')}
  useEffect(()=>{void load().catch(()=>toast.error('Could not load dashboards'))},[])
  useEffect(()=>{if(!selected)return setResults([]);void fetchDashboardResults(selected.id).then(value=>setResults(value.results)).catch(()=>toast.error('Could not load dashboard data'))},[selected])
  const create=async()=>{const name=window.prompt('Dashboard name','Untitled dashboard')?.trim();if(!name)return;setBusy(true);try{const item=await createDashboard({name,visibility:'private',widgets:[{id:'',type:'issue_count',title:'Issue count',position:0,width:1,config:{}}]});setItems(current=>[item,...current]);setSelectedId(item.id)}catch{toast.error('Could not create dashboard')}finally{setBusy(false)}}
  const patch=async(input:Parameters<typeof updateDashboard>[1])=>{if(!selected)return;const item=await updateDashboard(selected.id,input);setItems(current=>current.map(value=>value.id===item.id?item:value))}
  const addWidget=async(type:DashboardWidget['type'])=>{if(!selected)return;const option=widgetOptions.find(item=>item.type===type)!;await patch({widgets:[...selected.widgets,{id:'',type,title:option.label,position:selected.widgets.length,width:1,config:{}}]})}
  const removeWidget=async(id:string)=>{if(selected)await patch({widgets:selected.widgets.filter(widget=>widget.id!==id)})}
  const remove=async()=>{if(!selected||!window.confirm(`Delete “${selected.name}”?`))return;await deleteDashboard(selected.id);setItems(current=>current.filter(item=>item.id!==selected.id));setSelectedId('')}
  const subscribed=Boolean(selected?.subscriberIds.includes(data.viewer.id))
  return <main className="dashboards-shell">
    <header className="dashboards-header"><button className="mobile-menu" type="button" onClick={onOpenSidebar}>Menu</button><LayoutDashboard/><strong>Dashboards</strong><span/><button type="button" disabled={busy} onClick={()=>void create()}><Plus/>New dashboard</button></header>
    <div className="dashboards-layout"><aside className="dashboard-list" aria-label="Dashboards">{items.map(item=><button className={item.id===selected?.id?'active':''} key={item.id} onClick={()=>setSelectedId(item.id)} type="button"><strong>{item.name}</strong><small>{item.widgets.length} widgets · {item.visibility}</small></button>)}{items.length===0&&<div className="dashboard-empty">No dashboards yet</div>}</aside>
    <section className="dashboard-canvas">{selected?<><div className="dashboard-title"><div><input aria-label="Dashboard name" value={selected.name} onChange={event=>setItems(current=>current.map(item=>item.id===selected.id?{...item,name:event.target.value}:item))} onBlur={event=>void patch({name:event.target.value})}/><p>{selected.description||'A live view of workspace delivery data.'}</p></div><div className="dashboard-actions"><select aria-label="Visibility" value={selected.visibility} onChange={event=>void patch({visibility:event.target.value as Dashboard['visibility']})}><option value="private">Private</option><option value="workspace">Workspace</option><option value="team">Team</option></select><button title={subscribed?'Unsubscribe':'Subscribe'} aria-label={subscribed?'Unsubscribe':'Subscribe'} onClick={()=>void subscribeDashboard(selected.id,!subscribed).then(item=>setItems(current=>current.map(value=>value.id===item.id?item:value)))} type="button">{subscribed?<BellOff/>:<Bell/>}</button><button title={selected.shareToken?'Disable public link':'Create public link'} aria-label="Share dashboard" onClick={()=>void shareDashboard(selected.id,!selected.shareToken).then(async item=>{setItems(current=>current.map(value=>value.id===item.id?item:value));if(item.shareToken){const url=`${location.origin}/api/shared/dashboards/${item.shareToken}?workspace=${data.workspace.urlKey}`;await navigator.clipboard.writeText(url);toast.success('Public link copied')}})} type="button">{selected.shareToken?<Link/>:<Share2/>}</button><a title="Export CSV" aria-label="Export dashboard" href={dashboardExportURL(selected.id)} download><Download/></a><button className="danger" title="Delete dashboard" aria-label="Delete dashboard" onClick={()=>void remove()} type="button"><Trash2/></button></div></div>
      <div className="dashboard-add"><label>Add widget<select defaultValue="" onChange={event=>{if(event.target.value)void addWidget(event.target.value as DashboardWidget['type']);event.target.value=''}}><option value="" disabled>Select metric…</option>{widgetOptions.map(option=><option key={option.type} value={option.type}>{option.label}</option>)}</select></label></div>
      <div className="dashboard-grid">{results.map(result=><article className={result.widget.width===2?'wide':''} key={result.widget.id}><header><strong>{result.widget.title}</strong><button type="button" aria-label={`Remove ${result.widget.title}`} onClick={()=>void removeWidget(result.widget.id)}><Trash2/></button></header><WidgetValue result={result}/></article>)}</div>
    </>:<div className="dashboard-empty large"><LayoutDashboard/><strong>Create a dashboard</strong><p>Combine live issue, project, cycle, and SLA metrics.</p><button type="button" onClick={()=>void create()}><Plus/>New dashboard</button></div>}</section></div>
  </main>
}

function WidgetValue({result}:{result:DashboardWidgetResult}) {
  if(result.widget.type==='issue_count')return <div className="metric-number">{String((result.value as {count:number}).count)}</div>
  if(Array.isArray(result.value))return <div className="metric-list">{result.value.slice(0,8).map((row,index)=><div key={String((row as {id?:string}).id??index)}><span>{String((row as {name?:string}).name??'Item')}</span><strong>{'progress' in (row as object)?`${Math.round(Number((row as {progress:number}).progress)*100)}%`:String((row as {completed?:number;total?:number}).completed??0)+' / '+String((row as {total?:number}).total??0)}</strong></div>)}</div>
  const entries=Object.entries((result.value??{}) as Record<string,unknown>)
  return <div className="metric-list">{entries.length?entries.slice(0,10).map(([label,value])=><div key={label}><span>{label}</span><strong>{String(value)}</strong></div>):<span className="metric-muted">No data</span>}</div>
}
