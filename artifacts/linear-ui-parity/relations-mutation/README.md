# Project and initiative relation mutation audit

This audit used the signed-in `leozhengliu` Linear workspace and temporary data prefixed `Flow parity audit 2026-08-30`.

## Project relations

The full project relation lifecycle was measured in Linear: both dependency directions in project creation, the two-stage project picker, persisted relation rows, hover-only row menu, direction conversion, removal, refresh persistence, and keyboard navigation. The raw menu DOM and computed dimensions are in `linear-project-relation-menu.json`; its viewport screenshot is `linear-project-relation-menu.png`.

Flow was changed to match the measured sidebar behavior. Relations are grouped under one `Blocked by` or `Blocking` heading, use a 28px project row, expose a 24px three-dot button only on hover/focus/open, and provide Linear's ordered `Change to …` and `Remove dependency` menu actions. The previously invented dependency graph button and always-visible trash control were removed.

## Initiative relations

Two real initiatives were created and both the creation editor and detail page were traversed. No parent/sub-initiative control appeared in the create form, properties sidebar, Add menu, or initiative action menu. This is a plan boundary, not missing discovery: Linear documents sub-initiatives as Enterprise-only, while the audited workspace is on a Business trial: https://linear.app/docs/sub-initiatives.

Flow's existing initiative relation API is therefore not presented as a pixel-replicated Linear UI in this evidence set. Doing so without an Enterprise workspace would violate the measured-state gate.

## Cleanup

Both temporary projects and both temporary initiatives were deleted through Linear's confirmation dialogs. Linear retains deleted records in Recently deleted according to its normal 30-day recovery behavior.
