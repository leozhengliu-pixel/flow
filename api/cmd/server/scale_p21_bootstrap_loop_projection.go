package main

import (
	"context"
	"errors"

	"flow/api/internal/domain"
)

// The instruction-derived values come from scaleP21LoopText; Instructions may
// remain an opaque body marker until the owning bootstrap reader binds output.
func scaleP21PresentLoop(counts map[string]int, loop domain.Loop, instructionsHash, fallbackDescription string) domain.Loop {
	if loop.Status == "" {
		loop.Status = "published"
	}
	loop.CodeAccess = loopCodeAccess(loop)
	if loop.Status == "published" && loop.Version == 0 {
		loop.Version, loop.VersionID = 1, loopVersionID(loop.ID, 1)
	}
	loop.TriggerConfig = normalizeLoopTriggerConfig(loop.TriggerType, loop.TriggerConfig)
	if loop.ConnectorIDs == nil {
		loop.ConnectorIDs = []string{}
	}
	if loop.Description == "" && loop.Status == "published" {
		loop.Description = fallbackDescription
	}
	loop.RunCount30d = counts[loop.ID]
	loop.SlugID = domain.LoopSlugID(loop)
	stale := loop.OutputContract != nil && loop.OutputContract.InstructionsHash != "" && loop.OutputContract.InstructionsHash != instructionsHash
	if loop.OutputContract != nil {
		contract := *loop.OutputContract
		contract.Stale = stale
		loop.OutputContract = &contract
	}
	if loop.SuggestedOutputContract != nil && (loop.SuggestedOutputContract.InstructionsHash != instructionsHash || !stale) {
		loop.SuggestedOutputContract = nil
	}
	return loop
}

// The owner supplies decoded raw instruction bytes without materializing a
// string. A failed read keeps the original loop slice, never a presented prefix.
func scaleP21SanitizeBootstrap(ctx context.Context, data *domain.Bootstrap, chunks func(context.Context, string, func([]byte) error) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data.Loops) > 0 && chunks == nil {
		return errors.New("bootstrap loop instruction reader is nil")
	}
	loops := data.Loops
	data.Loops = nil
	sanitizeBootstrap(data)
	data.Loops = loops
	if len(loops) == 0 {
		return ctx.Err()
	}
	presented := make([]domain.Loop, len(loops))
	for index, loop := range loops {
		hash, description, err := scaleP21LoopText(ctx, loop.Name, func(visit func([]byte) error) error {
			return chunks(ctx, loop.Instructions, visit)
		})
		if err != nil {
			return err
		}
		presented[index] = scaleP21PresentLoop(nil, loop, hash, description)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data.Loops = presented
	return nil
}
