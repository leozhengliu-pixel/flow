package main

import (
	"context"
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) chatSendGroupHelp(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, command chatInboundCommand) error {
	binding, err := s.chatBindingForChat(ctx, record, command.ChatID)
	if err != nil {
		var permanent chatPermanentError
		var refusal chatGroupRefusal
		if !errors.As(err, &permanent) && !errors.As(err, &refusal) && !errors.Is(err, store.ErrAuthForbidden) {
			return err
		}
		binding = domain.ChatBinding{}
	}
	language := chatGroupLanguage(record, binding)
	elements := []map[string]any{}
	if binding.AllowCreate {
		elements = append(elements, chatCardText(chatT(language, "help_create")))
	}
	if binding.RequestsEnabled() {
		elements = append(elements, chatCardText(chatT(language, "help_request")))
		if binding.Requests.Emoji != "" {
			elements = append(elements, chatCardText(chatT(language, "help_reaction", binding.Requests.Emoji)))
		}
	}
	if binding.AllowUpdate {
		elements = append(elements, chatCardText(chatT(language, "help_update")))
	}
	if len(elements) == 0 {
		elements = append(elements, chatCardText(chatT(language, "help_off")))
	}
	message := chatInteractive(chatCard(chatT(language, "help_title"), elements...))
	message["receive_id"], message["uuid"] = command.OpenID, chatKey(job.DedupKey, "help")[:32]
	return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", message, nil)
}
