# Flow motion system

## Profiles

`web/src/lib/motion-presets.ts` records the distinct reference profiles:

- Generic surfaces and collapses: Motion spring, 300ms, no bounce.
- Fast creation: 350ms enter / 400ms exit with a pointer origin; 300ms without one.
- Popovers: tension 1500 on enter, 2000 on exit; friction 100, precision .01.
- Persisted milestones: React Spring keyed transitions, tension 2000, friction 100.
- Lightweight feedback: 150ms color/border transitions; no global list-row layout animation.

There is deliberately no universal route fade. Navigation scheduling and code
preloading remain separate from visual animation.

## Implementation

`FlowMotionProvider` installs reduced-motion configuration and records pointer
activation. Keyboard interaction clears that origin. Fast-creation surfaces
capture the origin once per opening and measure their unanimated final position.
The origin is not inferred from a stale pointer position after keyboard commands.

Radix still owns floating-element placement, focus management and CSS Presence.
`data-flow-motion` selects the enter/exit profile in `styles/motion.css`. These
profiles use individual scale/translate properties so they do not overwrite
existing positioning transforms. Fast-create surfaces compensate their transform
origin for existing centering translations.

Popover curves are sampled from React Spring and compiled to CSS `linear()`;
they are not one live React Spring controller per menu instance. The generic
surface curve is sampled from Motion. Reproduce the curves with
`node scripts/print-motion-curves.mjs` in `web/`.

`AnimatedCollapse` uses Motion's presence lifecycle and dimensions/opacity.
Integrated regions include workspace/team/favorite sidebar sections, team-home
resource sections, project-sidebar sections and draft milestone folding.
Fully open regions restore visible overflow so they do not clip menus.

`AnimatedMilestones` uses React Spring, stable persisted IDs and measured inner
heights. It retains leaving rows, makes them inert, and restores natural height
and overflow after entering. Both project overview and project sidebar use it.

Custom portals must keep their owning component mounted for the exit duration.
`useExitPresence` handles this for project creation, the application issue-creation
mount and the shared issue-details panel. Reopening cancels the pending removal.
The action-dialog host preserves its last content during exit instead of fading
out an empty shell.

## Verification status

- `npm run check:motion` statically checks all 332 direct Radix content/overlay
  declarations for an explicit motion profile. This is also part of lint.
- Unit tests cover retention, rapid reopen, reduced motion, stable milestone IDs,
  removal, and separate physical presets.
- Static coverage is not proof of animation completion in every branch. In
  particular, a parent that conditionally unmounts an entire custom dialog can
  bypass Radix Presence; new custom dialogs must use a retained parent.
- Live frame sampling and desktop/mobile screenshots for this change are pending:
  computer-use verification was blocked by the Mac being locked. No visual-parity
  or all-interaction-state sign-off is claimed yet.
