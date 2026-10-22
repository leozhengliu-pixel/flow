package coding

import (
	"context"
	"log/slog"
	"time"
)

// Pending turn scopes. When the scope parser is unsure about a follow-up and
// a model is configured, Prompt queues the turn at once with the parser's
// answer and a pending mark (Turn.ScopePendingSince), and settles the
// model's answer in the background, so sending a follow-up never waits on
// the model:
//
//   - settleTurnScope asks the model (detached from the request, bounded by
//     TurnScopeModelTimeout) and rewrites the queued turn with the answer, or
//     just clears the mark on an error or timeout (the parser's answer
//     stays). The rewrite is a compare-and-swap on the undelivered item, so
//     it never touches a turn that was delivered or changed meanwhile.
//   - Dispatch (bootstrap and inbox) never delivers a pending turn, and the
//     turns behind it wait too (one turn at a time, in order). A mark older
//     than TurnScopePendingStale (the API restarted while the model was
//     asked) falls back to the parser's answer at dispatch, so a turn is
//     never stuck.

// TurnScopePendingStale is how long a pending scope may wait for its model
// answer before dispatch falls back to the parser's answer: the model
// timeout plus a margin for the background write.
const TurnScopePendingStale = TurnScopeModelTimeout + 4*time.Second

// turnScopeSettleWrite bounds the background write of a settled scope.
const turnScopeSettleWrite = 5 * time.Second

// settleTurnScope settles the pending scope of the queued turn (seq,
// turnID) in the background; fallback is the parser's decision. ctx only
// lends its values (the request's cancellation does not reach the call).
func (s *Service) settleTurnScope(ctx context.Context, sessionID string, seq int64, turnID, request string, fallback TurnScopeDecision) {
	model := s.TurnScopeModel
	detached := context.WithoutCancel(ctx)
	go func() {
		askCtx, cancel := context.WithTimeout(detached, TurnScopeModelTimeout)
		decision := AskTurnScopeModel(askCtx, model, request, fallback)
		cancel()
		writeCtx, cancel := context.WithTimeout(detached, turnScopeSettleWrite)
		defer cancel()
		settled, err := s.store.UpdateUndeliveredCodingTurn(writeCtx, sessionID, seq, func(turn *Turn) bool {
			if turn.ID != turnID || turn.ScopePendingSince == 0 {
				return false
			}
			turn.Scope, turn.ScopeSource, turn.ScopePendingSince = decision.Scope, decision.Source, 0
			return true
		})
		switch {
		case err != nil:
			// The mark stays; dispatch falls back once it is stale.
			slog.Warn("coding turn scope not settled", "session", sessionID, "turn", turnID, "err", err)
		case settled:
			slog.Info("coding turn scope settled", "session", sessionID, "turn", turnID, "scope", decision.Scope, "source", decision.Source, "rule", decision.Rule)
		default:
			slog.Info("coding turn scope settled elsewhere", "session", sessionID, "turn", turnID)
		}
		s.inbox.notify(sessionID)
	}()
}

// turnReady reports whether an undelivered turn item may be delivered: its
// scope is settled, or its pending mark is stale and falls back to the
// parser's answer now (the returned item carries the settled turn).
func (s *Service) turnReady(ctx context.Context, sessionID string, item InboxItem) (InboxItem, bool, error) {
	if item.Turn == nil || item.Turn.ScopePendingSince == 0 {
		return item, true, nil
	}
	pendingSince := time.UnixMilli(item.Turn.ScopePendingSince)
	if s.now().Sub(pendingSince) < TurnScopePendingStale {
		return item, false, nil
	}
	turnID := item.Turn.ID
	var settled Turn
	ok, err := s.store.UpdateUndeliveredCodingTurn(ctx, sessionID, item.Seq, func(turn *Turn) bool {
		if turn.ID != turnID || turn.ScopePendingSince == 0 {
			return false
		}
		turn.ScopePendingSince = 0
		settled = *turn
		return true
	})
	if err != nil || !ok {
		// Settled or changed concurrently: the next read sees it.
		return item, false, err
	}
	slog.Info("coding turn scope fell back to the parser", "session", sessionID, "turn", turnID, "scope", settled.Scope, "source", settled.ScopeSource, "pendingSince", pendingSince)
	item.Turn = &settled
	return item, true, nil
}
