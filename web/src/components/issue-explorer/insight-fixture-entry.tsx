import { createRoot } from 'react-dom/client'
import { Fixture } from './insight-visual-fixture'
import '../../styles/tokens.css'
import '../../styles/foundations.css'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'

const params = new URLSearchParams(location.search)
document.documentElement.dataset.theme = params.get('theme') === 'light' ? 'light' : 'dark'
localStorage.setItem('flow:locale', 'en-US')
localStorage.setItem('flow:saved-view:my-issues-assigned:insights-intro', 'dismissed')

if (params.has('unique')) localStorage.setItem('workspace:my-issues:assigned:insights', JSON.stringify({ measure: 'issueCount', slice: 'creator', segment: 'none' }))
else localStorage.setItem('workspace:my-issues:assigned:insights', JSON.stringify({ measure: params.get('measure') ?? 'issueCount', slice: 'status', segment: params.get('segment') ?? 'none' }))
const root = createRoot(document.getElementById('root')!)
root.render(<I18nProvider><TooltipProvider><Fixture/></TooltipProvider></I18nProvider>)
import.meta.hot?.dispose(() => root.unmount())
