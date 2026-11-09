# Media

`flow-intro.mp4` (H.264), `flow-intro.webm` (VP9), `flow-intro-poster.jpg` and `flow-intro.en.vtt` (captions) are the
welcome notification's intro video (`src/components/inbox/hosts/welcome-video-player.tsx`). They are Flow's own UI,
recorded at 1600x900 by `scripts/render-intro-video.mjs`: it seeds a throwaway demo workspace, drives the app with
Playwright (create an issue, open it, switch to the board, drag it to In Progress, ask the Flow Agent), captures CDP
screencast frames and stitches them with ffmpeg. The script's header lists the private API and dev server it needs;
run `node scripts/render-intro-video.mjs` from `web/` to regenerate all four files.
