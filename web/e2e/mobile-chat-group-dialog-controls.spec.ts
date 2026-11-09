import { test } from './workspace-fixture'
import { exerciseChatGroupDialog } from './chat-group-dialog-controls'

test.use({ viewport: { width: 390, height: 844 } })

test('every control of the project connect-group dialog works on a phone with native selects', async ({ page, request, workspaceKey, isMobile }) => {
  await exerciseChatGroupDialog({ page, request, workspaceKey, isMobile })
})
