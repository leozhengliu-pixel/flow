import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../../styles/tokens.css'
import { MyIssuesVisualFixture } from './my-issues-visual-fixture'

createRoot(document.getElementById('root')!).render(<StrictMode><MyIssuesVisualFixture/></StrictMode>)
