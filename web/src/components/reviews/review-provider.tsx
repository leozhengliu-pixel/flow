import { GitMerge, GitPullRequest } from "lucide-react";
import type { ComponentProps } from "react";

import { IntegrationBrandIcon } from "@/components/settings/integration-brand-icon";
import type { CodeReview } from "@/types/flow";

export function ReviewProviderIcon({
  provider,
  ...props
}: { provider: CodeReview["provider"] } & ComponentProps<"svg">) {
  const Icon = provider === "gitlab" ? GitMerge : GitPullRequest;
  return <Icon {...props} />;
}

/** Code host mark used by the repository pill (GitHub mark, full-colour GitLab logo). */
export function CodeHostIcon({ provider, ...props }: { provider: CodeReview["provider"] } & ComponentProps<"svg">) {
  if (provider === "gitlab") return <IntegrationBrandIcon provider="gitlab" size={14} />;
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true" {...props}>
      <path d="M8 1.4a6.75 6.75 0 0 0-2.13 13.15c.34.06.46-.14.46-.32v-1.3c-1.88.41-2.28-.8-2.28-.8-.31-.78-.75-.98-.75-.98-.61-.42.05-.41.05-.41.68.05 1.03.69 1.03.69.6 1.03 1.57.73 1.95.56.06-.43.24-.73.43-.9-1.5-.17-3.08-.75-3.08-3.34 0-.74.26-1.34.69-1.81-.07-.17-.3-.86.07-1.79 0 0 .57-.18 1.86.69A6.4 6.4 0 0 1 8 4.61c.58 0 1.15.08 1.69.23 1.29-.87 1.85-.69 1.85-.69.37.93.14 1.62.07 1.79.43.47.69 1.07.69 1.81 0 2.6-1.58 3.16-3.09 3.33.24.21.46.63.46 1.27v1.88c0 .18.12.39.46.32A6.75 6.75 0 0 0 8 1.4Z" fill="currentColor" />
    </svg>
  );
}
