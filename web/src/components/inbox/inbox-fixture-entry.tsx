import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { InboxPageStory } from './inbox-page.story'

const selectedId = new URLSearchParams(window.location.search).get('selected')

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <InboxPageStory initialSelectedId={selectedId} />
  </StrictMode>,
)
