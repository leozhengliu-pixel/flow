package main

import (
	"errors"
	"net/http"
	"strings"
)

var errChatBotUnpublished = errors.New("Enable and publish the application bot before connecting")

// Chat integration endpoints answer with a stable code next to the English
// message so the web app can show the reason in the member's language. The
// codes also travel in the identity OAuth redirect and describe a
// connection's last error.
var chatErrorCodes = map[string]string{
	"Chat configuration is busy; retry shortly":                                 "chat_busy",
	"Chat configuration is busy; restart authorization":                         "chat_busy",
	"Unsupported chat provider":                                                 "chat_unsupported_provider",
	"Workspace access required":                                                 "chat_workspace_access_required",
	"Interactive sign-in required":                                              "chat_interactive_sign_in_required",
	"Workspace policy unavailable":                                              "chat_policy_unavailable",
	"Could not verify session authentication":                                   "chat_session_unverified",
	"Your session does not meet the workspace authentication policy":            "chat_session_policy",
	"Workspace administrator required":                                          "chat_admin_required",
	"Could not read chat integration":                                           "chat_read_failed",
	"Could not read integration":                                                "chat_read_failed",
	"Could not read group bindings":                                             "chat_bindings_read_failed",
	"Configure FLOW_CONNECTOR_SECRET_KEY before connecting chat applications":   "chat_secret_key_missing",
	"Invalid application configuration":                                         "chat_invalid_configuration",
	"Could not read encrypted application credentials":                          "chat_credentials_unreadable",
	"Disconnect the current application before changing App ID":                 "chat_app_id_locked",
	"Could not create connection":                                               "chat_save_failed",
	"App Secret, Encrypt Key and Verification Token are required":               "chat_credentials_required",
	"Chat application check failed; verify app credentials and bot publication": "chat_check_failed",
	"Enable and publish the application bot before connecting":                  "chat_bot_unpublished",
	"Could not initialize notification cursor":                                  "chat_save_failed",
	"Application is already connected to another workspace":                     "chat_app_in_use",
	"Could not save encrypted application credentials":                          "chat_save_failed",
	"Could not save connection metadata":                                        "chat_save_failed",
	"Chat application not configured":                                           "chat_not_configured",
	"Could not save application health":                                         "chat_save_failed",
	"Authorize an application administrator to verify the tenant first":         "chat_tenant_unverified",
	"Invalid cursor": "chat_invalid_cursor",
	"The chat platform request failed; check the bot's permissions and retry": "chat_platform_failed",
	"Connect and authorize the application first":                             "chat_tenant_unverified",
	"Invalid group binding":                                                   "chat_invalid_binding",
	"Unsupported notification event":                                          "chat_invalid_binding",
	"Binding not found":                                                       "chat_binding_not_found",
	"Could not create binding":                                                "chat_save_failed",
	"Select a same-tenant internal group, an active public team and compatible public project/template": "chat_binding_scope",
	"Could not save binding; the group may already be bound":                                            "chat_binding_conflict",
	"Connection not found":                                                                             "chat_not_configured",
	"Configure the application first":                                                                  "chat_not_configured",
	"An administrator must verify the application tenant first":                                        "chat_tenant_unverified",
	"Could not begin authorization":                                                                    "chat_authorization_failed",
	"Could not save authorization state":                                                               "chat_authorization_failed",
	"Invalid authorization state":                                                                      "chat_authorization_expired",
	"Authorization expired":                                                                            "chat_authorization_expired",
	"Complete authorization in the same signed-in browser":                                             "chat_authorization_other_session",
	"Sign in again to bind your account":                                                               "chat_authorization_other_session",
	"Your workspace authentication policy no longer permits this binding":                              "chat_session_policy",
	"Workspace multi-factor authentication required":                                                   "chat_mfa_required",
	"Application connection changed; restart authorization":                                            "chat_authorization_expired",
	"Authorization revoked or expired":                                                                 "chat_authorization_expired",
	"Authorization already used":                                                                       "chat_authorization_expired",
	"Authorization declined":                                                                           "chat_authorization_declined",
	"Application credentials unavailable":                                                              "chat_credentials_unreadable",
	"Platform authorization failed; verify application OAuth configuration":                            "chat_platform_authorization_failed",
	"Could not verify platform identity":                                                               "chat_platform_authorization_failed",
	"This account belongs to another tenant":                                                           "chat_other_tenant",
	"Administrator authorization required":                                                             "chat_admin_authorization_required",
	"Application tenant is already connected to another workspace":                                     "chat_app_in_use",
	"This platform identity is already bound to another Flow account":                                  "chat_identity_in_use",
	"Application setup is incomplete; disconnect and configure it again":                               "chat_setup_incomplete",
	"Application credentials are unavailable; restore the encryption key or reconnect the application": "chat_credentials_unreadable",
}

func chatErrorCode(message string) string {
	if code, ok := chatErrorCodes[message]; ok {
		return code
	}
	if strings.HasPrefix(message, "Chat delivery: ") {
		return "chat_delivery_failed"
	}
	return "chat_error"
}

func writeChatError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": chatErrorCode(message)})
}
