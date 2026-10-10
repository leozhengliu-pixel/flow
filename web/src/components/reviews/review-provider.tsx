import { GitMerge, GitPullRequest } from "lucide-react";
import type { ComponentProps } from "react";

import type { CodeReview } from "@/types/flow";

export function ReviewProviderIcon({
  provider,
  ...props
}: { provider: CodeReview["provider"] } & ComponentProps<"svg">) {
  const Icon = provider === "gitlab" ? GitMerge : GitPullRequest;
  return <Icon {...props} />;
}
