# Feature tour

Scripted product demos for `docs/media/`. A clip is a small script that drives
the real app with Playwright (Chrome, 1440x900 CSS px at 2x), adds a prominent
cursor with click ripples and focus rings, captions, and a smooth zoom/pan
camera, and exports an MP4 plus a GIF with ffmpeg.

## One-time setup

1. Start the API with dev tokens and the local agent model (see
   `.claude/launch.json`: servers `go-proxy`, `api`, `web`). The API must run
   with `FLOW_DEV_AUTH_TOKENS=true` (the seed verifies demo accounts with the
   token the API returns) and `FLOW_AGENT_ENABLED=true` for the agent clips.
2. Seed the demo workspace (Acme: three teams, five invented people, about 35
   issues, four projects, two initiatives, three documents, a cycle):

   ```bash
   node scripts/feature-tour/seed.mjs
   ```

   It talks to `http://localhost:5173` (override with `FLOW_TOUR_URL`), uses its
   own accounts (`*@acme.test`), and stores credentials and sessions in
   `scripts/feature-tour/.state/` (git-ignored). It is safe to re-run. The demo
   accounts live on the `localhost` origin, so they do not interfere with a dev
   account that is signed in on `127.0.0.1`.
3. Needs `ffmpeg` and Chrome. Playwright comes from `web/node_modules`.

## Recording

```bash
node scripts/feature-tour/record.mjs --list          # clip names
node scripts/feature-tour/record.mjs                 # every clip
node scripts/feature-tour/record.mjs issues-board    # one or more clips
```

Output goes to `docs/media/<clip>.mp4` and `.gif`. Each GIF is tuned down
(fps, width, palette) until it fits `FLOW_TOUR_GIF_BUDGET` (default 5 MB).
`FLOW_TOUR_SKIP_GIF=1` writes only the MP4, which is much faster while
iterating. Each clip restores the demo data it changed, so clips can be
recorded in any order and any number of times.

## Anatomy of a clip

`clips/<name>.mjs` default-exports `{ title, subtitle, eyebrow, startPath,
viewport?, speed?, storage?, prepare?, cleanup?, run }`.

- `prepare(demo)` / `cleanup(demo)` use the helpers in `lib/demo.mjs` to put data
  back (documents, saved views, inbox, agent chats, issues created by the agent).
- `run(stage)` is the choreography. Targets are Playwright locators, selectors,
  or `'t:Exact text'`.
  - `stage.click(target, { expect })`, `moveTo`, `type`, `press`, `drag`
  - `stage.focusRect({ x, y, w, h })` / `unfocus()` move the camera (viewport
    coordinates of the rect to frame); `stage.caption('text')`
  - `stage.untilQuiet()` waits for a streamed agent answer to finish

The page always renders at 1x layout, so menus, popovers and drag and drop behave
exactly as in production. The camera and captions are applied by ffmpeg from a
log of keyframes, cropping the 2x capture, so zoomed views stay sharp.
