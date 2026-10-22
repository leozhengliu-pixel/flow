package main

import (
	"context"
	"errors"
	"strings"

	"flow/api/internal/coding"
)

// errTurnScopeModelOff is returned when Flow Agent has no model configured;
// the coding core then keeps the scope parser's answer.
var errTurnScopeModelOff = errors.New("flow agent model is not configured")

// classifyCodingTurnScope is the coding core's TurnScopeModel: one short call
// to the configured Flow Agent model, without tools, for follow-up requests
// the scope parser is unsure about. coding.AskTurnScopeModel bounds the input
// and the time (coding.TurnScopeModelTimeout) and falls back to the parser on
// any error or unusable answer; the coding core calls it in the background
// after the turn is queued (coding/turn_scope_pending.go).
func (s *server) classifyCodingTurnScope(ctx context.Context, request string) (string, error) {
	if !s.turnScopeModelConfigured() {
		return "", errTurnScopeModelOff
	}
	turn, err := s.requestAgentTurnWithoutTools(ctx, []agentProviderMessage{
		{Role: "system", Content: coding.TurnScopeModelPrompt},
		{Role: "user", Content: "Latest message:\n<message>\n" + request + "\n</message>"},
	})
	if err != nil {
		return "", err
	}
	return turnScopeAnswer(turn.Text)
}

// turnScopeModelConfigured reports whether Flow Agent has a model to settle
// unsure follow-up scopes with.
func (s *server) turnScopeModelConfigured() bool {
	return s.agent.Enabled && strings.TrimSpace(s.agent.Model) != ""
}

// turnScopeAnswer reads the one-word answer (tolerating quotes, markdown and
// a trailing period); anything else is an error.
func turnScopeAnswer(text string) (string, error) {
	word := strings.Trim(strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0]), "`*\"'.:;!。 ")
	scope, err := coding.NormalizeTurnScope(word)
	if err != nil || scope == "" {
		return "", errors.New("unusable scope answer")
	}
	return scope, nil
}
