import type { ReviewEvent } from "@/types/flow";

export function formatReviewCommentMarkdown(event: ReviewEvent): string {
  const location =
    event.path != null
      ? event.line != null
        ? `\`${event.path}:${event.line}\``
        : `\`${event.path}\``
      : "";
  const body = event.body?.trim() || "(empty comment)";
  return location ? `${location}\n\n${body}` : body;
}

export function buildAgentDispatchPrompt(event: ReviewEvent, reviewId: string): string {
  const markdown = formatReviewCommentMarkdown(event);
  return [
    `Help with this pull request comment (review ${reviewId}).`,
    "",
    markdown,
  ].join("\n");
}
