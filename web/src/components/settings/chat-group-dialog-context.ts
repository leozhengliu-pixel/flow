import { createContext, useContext } from 'react'

export const ChatGroupDialogContext = createContext(false)
/** True inside the connection dialog, where the binding form follows the notification panel's compact layout. */
export const useInChatGroupDialog = () => useContext(ChatGroupDialogContext)
