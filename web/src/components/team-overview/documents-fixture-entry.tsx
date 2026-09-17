import { createRoot } from 'react-dom/client'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { TeamOverviewPage } from './team-overview-page'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'
import '../../styles/tokens.css'
import '../../styles/foundations.css'

const params = new URLSearchParams(location.search)
document.documentElement.dataset.theme = params.get('theme') === 'light' ? 'light' : 'dark'
localStorage.setItem('flow:locale', params.get('locale') === 'zh-CN' ? 'zh-CN' : 'en-US')
const documents: FlowDocument[] = Array.from({ length: Math.min(10000, Math.max(0, Number(params.get('count')) || 0)) }, (_, i) => ({
  id: `doc-${i}`, slugId: `doc-${i}`, title: `Document ${String(i).padStart(5,'0')}`, content: '', creator: viewer, teamIds: ['team-1'], projectIds: i % 3 === 0 ? ['project-1'] : [], subscriberIds: [], favorite: false, revisions: [], createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-02T00:00:00Z',
}))
const data = makeBootstrap({ documents, favorites: [], teamMembers: [], teamSettings: {}, members: [], subscriptions: [] })
const originalFetch = window.fetch
window.fetch = (input, init) => {
  const path = String(input)
  if(path.includes('/api/teams/team-1/resources')) return Promise.resolve(new Response(JSON.stringify({resources:[],sections:[]}),{headers:{'Content-Type':'application/json'}}))
  if(path.endsWith('/api/documents') && init?.method === 'POST') return Promise.resolve(new Response(JSON.stringify({id:'new',slugId:'new',...JSON.parse(String(init.body))}),{headers:{'Content-Type':'application/json'}}))
  return originalFetch(input,init)
}
const root = createRoot(document.getElementById('root')!)
root.render(<I18nProvider><TooltipProvider><div className="documents-fixture"><TeamOverviewPage data={data} team={data.teams[0]} view="documents" onReload={async()=>{document.title='Documents refreshed'}} onNavigate={path=>{document.title=path}} onOpenSidebar={()=>{document.title='Open sidebar'}}/></div></TooltipProvider></I18nProvider>)
import.meta.hot?.dispose(()=>{root.unmount();window.fetch=originalFetch})
