import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import type { BootstrapData } from '@/types/flow'

/** The page shell the settings editors need to show mentions: i18n, router and the workspace store. */
export function Shell({ children, data }: { children: ReactNode; data: BootstrapData }) {
  return <I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>{children}</WorkspaceStoreProvider></MemoryRouter></I18nProvider>
}
