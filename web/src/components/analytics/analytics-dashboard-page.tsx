import { Download, LayoutDashboard, LoaderCircle, TrendingUp } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { createExport, exportDownloadUrl, getAnalyticsOverview } from '@/lib/api'
import './analytics-dashboard-page.css'

type Overview = { issues?: { total?: number; active?: number }; status?: Record<string, number>; team?: Record<string, number>; throughput?: Array<{ date: string; count: number }>; averageCycleTimeHours?: number; projects?: number; cycles?: number }

export function AnalyticsDashboardPage() {
  const [overview, setOverview] = useState<Overview>(); const [loading, setLoading] = useState(true); const [exportId, setExportId] = useState<string>()
  const load = async () => { setLoading(true); try { setOverview(await getAnalyticsOverview() as Overview) } finally { setLoading(false) } }
  useEffect(() => { void load() }, [])
  const max = useMemo(() => Math.max(1, ...(overview?.throughput ?? []).map(point => point.count)), [overview])
  const exportData = async () => { const job = await createExport('json', false); setExportId(job.id) }
  if (loading && !overview) return <main className="main-panel analytics-dashboard"><LoaderCircle className="analytics-dashboard__spin" size={18}/></main>
  return <main className="main-panel analytics-dashboard"><header><div><h1>Analytics</h1><p>Workspace delivery and throughput</p></div><Link className="analytics-dashboard__download" to="../dashboards"><LayoutDashboard size={14}/>Dashboards</Link><button onClick={() => void exportData()} type="button"><Download size={14}/>{exportId ? 'Export queued' : 'Export data'}</button>{exportId && <a className="analytics-dashboard__download" href={exportDownloadUrl(exportId)} download>Download latest</a>}</header><section className="analytics-dashboard__metrics"><Metric label="Total issues" value={overview?.issues?.total ?? 0}/><Metric label="Active issues" value={overview?.issues?.active ?? 0}/><Metric label="Projects" value={overview?.projects ?? 0}/><Metric label="Cycles" value={overview?.cycles ?? 0}/><Metric label="Avg cycle time" value={`${Math.round(overview?.averageCycleTimeHours ?? 0)}h`}/></section><section className="analytics-dashboard__grid"><article><h2>Throughput</h2><div className="analytics-dashboard__chart" aria-label="Issue throughput" role="img">{(overview?.throughput ?? []).map(point => <span key={point.date} style={{ height: `${Math.max(4, point.count / max * 100)}%` }} title={`${point.date}: ${point.count}`}/>)}</div></article><article><h2>Status</h2><ul>{Object.entries(overview?.status ?? {}).sort((a,b) => b[1] - a[1]).map(([label,count]) => <li key={label}><span>{label}</span><strong>{count}</strong></li>)}</ul></article><article><h2>By team</h2><ul>{Object.entries(overview?.team ?? {}).sort((a,b) => b[1] - a[1]).map(([label,count]) => <li key={label}><span>{label}</span><strong>{count}</strong></li>)}</ul></article></section><div className="analytics-dashboard__hint"><TrendingUp size={15}/>Metrics are calculated from persisted workspace events.</div></main>
}
export default AnalyticsDashboardPage
function Metric({ label, value }: { label: string; value: string | number }) { return <div><span>{label}</span><strong>{value}</strong></div> }
