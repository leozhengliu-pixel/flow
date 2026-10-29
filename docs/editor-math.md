# Math Block Review

Use an editable issue and document in your workspace. Replace the route placeholders
below with their actual workspace key and identifiers.

1. Open `/<workspace>/issue/<identifier>/<slug>`. Click an empty description line
   and type `/`. Check that **LaTeX math block** follows **Diagram** and precedes
   **Collapsible section**. Type `latex` to leave only the math item, then click it.
   The empty source block should have the caret, without a preview.
2. Type `\frac{a_b}{c_d}`. Click the issue title: the source should be replaced by
   a centered fraction. Click the fraction to edit its source again. Enter should
   insert a source newline, not leave the formula or submit an issue.
3. Hover over the block and click the left-gutter **Formula actions** button.
   Try **Copy formula** and paste into plain text, **Show source**, **Fullscreen**
   and its close button, then **Delete** followed by Undo.
4. Type `$x^2$` in an ordinary paragraph. It must stay literal text. On a separate
   empty paragraph, type `$$` and press Enter: only that line should become math.
5. Open `/<workspace>/document/<slug>`. Repeat insertion, then open the same
   document in a second browser session. Edit different parts of the source in
   both sessions, leave the formula, and reload. Both changes should survive.
   Open document history and view a version containing the formula; restore it
   and check that the source is still editable.
6. On the issue, click **Leave a comment**, type `$$`, Enter, `x^2`, Enter, `+ y`.
   It must not send on either Enter. Click **Submit comment**; the posted comment
   should render the formula. Edit the comment and verify both source lines.
7. Open `/<workspace>/inbox` and select a notification for that issue. Its rich
   description/comment preview should render the same formula without editing it.
8. Switch the interface language to Chinese. The slash item should read
   **LaTeX 数学公式块** and the formula actions should be translated. Repeat the
   layout checks in light/dark themes and a narrow mobile viewport; long formulas
   should scroll inside the block rather than widen the page.
9. Against an enforced-CSP server, reload with browser DevTools open. Formula
   CSS and KaTeX font requests must be same-origin, with no CSP violations.
   Through MCP, save a document whose content contains a `$$` block and a literal
   `$x^2$` paragraph, get it, then save the returned Markdown unchanged. Verify
   the formula is still a block and the paragraph is still literal text.

## Automated Checks

From `web/`, with the worktree's own dependencies installed:

```sh
export PATH=~/.nvm/versions/node/v24.7.0/bin:$PATH
~/.flow-local/heavy.sh npx vitest run --maxWorkers=2 \
  src/components/issue/editor/math-extension.test.tsx \
  src/components/editor/composer-send.test.tsx \
  src/components/editor/markdown-paste/markdown-paste.test.ts \
  src/components/documents/document-markdown-doc.test.ts \
  src/components/documents/document-history-diff.test.ts \
  src/components/editor/mentions/mention-model.test.ts \
  src/components/issue/issue-description-editor-blocks.test.tsx \
  src/components/issue/issue-description-collab-sync.test.tsx \
  src/i18n/editor-coverage.test.ts
~/.flow-local/heavy.sh npx tsc -b
~/.flow-local/heavy.sh npm run lint
~/.flow-local/heavy.sh env NODE_OPTIONS=--max-old-space-size=2048 npx vite build
```

From `api/`:

```sh
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./cmd/server \
  -run 'TestMCPMath|TestSecurityHeadersWebAppPolicyHashesInlineScripts' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go vet -p 1 ./...
```
