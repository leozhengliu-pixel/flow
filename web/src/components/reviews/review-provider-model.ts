import type { CodeReview } from "@/types/flow";

export function reviewProviderNoun(provider: CodeReview["provider"]) {
  return provider === "gitlab" ? "merge request" : "pull request";
}

export function reviewProviderIdentifier(review: Pick<CodeReview, "provider" | "number">) {
  return `${review.provider === "gitlab" ? "!" : "#"}${review.number}`;
}
