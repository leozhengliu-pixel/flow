import { test } from './workspace-fixture'
import { exerciseChatGroupDialog } from './chat-group-dialog-controls'

test('every control of the project connect-group dialog takes real clicks and keys', async ({ page, request, workspaceKey, isMobile }) => {
  await exerciseChatGroupDialog({ page, request, workspaceKey, isMobile })
})
