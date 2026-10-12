# Application Agent Verification

Verified on 2026-10-12 using an isolated local database and an external Node.js
application worker. Test credentials stayed in process memory. Model requests
contained synthetic test issues, not workspace data from another service.

## Real Model Conversation

The external example used DeepSeek-V4.1-Flash (`deepseek-flash`) through the
DeepSeek streaming API. The following sequence ran against the real Flow API
and the rendered application in the existing Chrome instance:

1. Register an application, enable client credentials and obtain an app token.
2. Mention the application using a structured mention in an issue comment.
3. Receive incremental reasoning and Markdown output in that comment thread.
4. Ask for a language using a real model tool call; wait for a human answer.
5. Select a language without sending it, then press Continue.
6. Complete the response with a rendered Markdown list.
7. Send another prompt after completion and receive another response in the
   same thread. Reloading the page preserves the conversation.
8. Start another generation, then stop it from the UI. The task becomes canceled
   and retains that status without a late response overwriting it.
9. React to a generated message, reload the page, and remove the reaction.
   Feedback persists and does not resume the canceled or completed task.

The observed first continuation reached active after approximately 100 ms,
rendered streamed text before completion, and completed in approximately 2.9 s.
These are observations from one local request, not latency guarantees. Model
latency and network conditions affect subsequent requests.

## Rendered Measurements

Desktop comparisons used a 1470 x 708 CSS-pixel viewport at device pixel ratio 2.
Both light and dark appearances were measured after rendering, with screenshots
kept outside the repository. App identity, avatars and model-generated text
intentionally differ from the reference.

| Element | Reference | Flow |
| --- | --- | --- |
| Agent message width | 682.867 px | 682.859 px |
| Content left offset within message | 43 px | 43 px |
| Message padding | 14 px / 16 px | 14 px / 16 px |
| Header avatar | 18 px | 18 px |
| Header name / time | 13 px | 13 px |
| Body text | 15 px / 24 px, weight 450 | 15 px / 24 px, weight 450 |
| Resolved single-line question row | 120 px | 120 px |
| Selection button | 32 px high, pill radius | 32 px high, pill radius |
| Collapsed reply footer | 46 px + separator | 46 px + separator |
| List item spacing | 6 px | 6 px |

Dark and light body, name, timestamp, selection, container and border colors
use separately measured tokens. A narrow 390 x 844 viewport also passed the
horizontal-overflow check for messages, choices and the reply area.

These measurements establish parity for the listed properties and tested
states. They do not assert that every possible application signal, hover action,
browser font configuration or integration UI is pixel-identical.

## Automated Checks

- Full Go test suite passed.
- Application, OAuth, external worker and store tests passed with the race
  detector. Coverage includes stale output rejection, stream cancellation,
  explicit approvals, private-team access and credential revocation.
- Focused frontend Agent, comment, OAuth and credential tests passed.
- Frontend lint, architecture checks and production build passed.
- The standalone example passed the Node.js syntax check.

The full frontend run passed 1068 of 1070 tests (269 of 270 files). Two existing
Cycle date tests failed in the machine's America/Los_Angeles timezone: their
UTC-midnight fixture dates render on the previous local date; those Cycle tests
pass with `TZ=UTC`. Agent, comment,
application and OAuth tests passed. Cycle date behavior is outside this change.

The example supports a deterministic summary without a provider key, making its
base installation and delegation test independent of a paid external service.
Its streaming test uses a controlled provider and verifies multiple output
chunks, clarification, conversation continuation and provider cancellation.

See [the developer guide](developers.md), [中文开发者指南](developers.zh-CN.md)
and [the executable example](examples/application-agent.mjs).
