import { useI18n } from "@/i18n/i18n";
import { fillTemplate } from "@/lib/fill-template";
import type { ReviewEvent } from "@/types/flow";

/** Which code-host identity a Flow-originated review action was posted under. */
export function PostedAsCaption({ postedAs }: { postedAs?: ReviewEvent["postedAs"] }) {
  const { t } = useI18n();
  if (!postedAs) return null;
  const provider = postedAs.provider === "gitlab" ? "GitLab" : "GitHub";
  const login = <span data-i18n-ignore>@{postedAs.login}</span>;
  return (
    <span className="review-posted-as">
      {postedAs.via === "member"
        ? fillTemplate(t("Posted to {provider} as {login}"), { provider: <span data-i18n-ignore>{provider}</span>, login })
        : fillTemplate(t("Posted as {login} via the workspace integration"), { login })}
    </span>
  );
}
