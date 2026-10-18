package main

import "net/http"

// LLM proxy (api/internal/llmproxy) and commit signing for sandboxes.
//
// Owner: track E (LLM proxy & signing). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingLLMProxyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/llm-proxy/anthropic/v1/messages", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/anthropic/v1/messages/count_tokens", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/openai/v1/responses", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/openai/v1/chat/completions", s.codingLLMProxy)
	mux.HandleFunc("GET /internal/llm-proxy/openai/v1/models", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/opensource/v1/chat/completions", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/opensource/v1/responses", s.codingLLMProxy)
	mux.HandleFunc("GET /internal/llm-proxy/opensource/v1/models", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/coding-runtime/sign", s.codingRuntimeSign)
}

// codingLLMProxy — TODO(track E).
//
//	POST /internal/llm-proxy/anthropic/v1/messages: Anthropic Messages (Claude Code).
//	POST /internal/llm-proxy/anthropic/v1/messages/count_tokens: Anthropic token counting.
//	POST /internal/llm-proxy/openai/v1/responses: OpenAI Responses (Codex).
//	POST /internal/llm-proxy/openai/v1/chat/completions: OpenAI chat completions.
//	GET /internal/llm-proxy/openai/v1/models: Model list for the session.
//	POST /internal/llm-proxy/opensource/v1/chat/completions: Open-source (OpenAI-compatible) models.
//	POST /internal/llm-proxy/opensource/v1/responses: Open-source models over the Responses API.
//	GET /internal/llm-proxy/opensource/v1/models: Open-source model list.
func (s *server) codingLLMProxy(w http.ResponseWriter, r *http.Request) { codingNotImplemented(w, r) }

// codingRuntimeSign — TODO(track E).
//
//	POST /internal/coding-runtime/sign: Signs a commit payload with the requesting user's key (ssh or openpgp).
func (s *server) codingRuntimeSign(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}
