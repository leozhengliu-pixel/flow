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
	elements := []map[string]any{}
	if binding.AllowCreate {
		elements = append(elements, chatCardI18nText("Create an issue: @Flow create issue <title>, or use the message shortcut.", "创建事项：@Flow 创建事项 <标题>，或使用消息快捷操作。"))
	}
	if binding.RequestsEnabled() {
		elements = append(elements, chatCardI18nText("Submit a request: @Flow request <text> (提需求 / 需求), or use Submit as request in the message shortcut.", "提交需求：@Flow 提需求 <内容>（也可用「需求」或 request），或在消息快捷操作中选择「提交为需求」。"))
		if binding.Requests.Emoji != "" {
			elements = append(elements, chatCardI18nText("Request reaction: "+binding.Requests.Emoji, "需求表情："+binding.Requests.Emoji))
		}
	}
	if binding.AllowUpdate {
		elements = append(elements, chatCardI18nText("Update issues with their card controls; reply in an issue thread to comment.", "通过事项卡片更新事项；在事项话题中回复即可评论。"))
	}
	if len(elements) == 0 {
		elements = append(elements, chatCardI18nText("Group actions are off. A team owner can configure this group's Flow binding.", "此群未开启群内操作，团队管理员可配置此群的 Flow 绑定。"))
	}
	message := chatInteractive(chatCardI18n("Flow in this group", "此群中的 Flow", elements...))
	message["receive_id"], message["uuid"] = command.OpenID, chatKey(job.DedupKey, "help")[:32]
	return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", message, nil)
}
