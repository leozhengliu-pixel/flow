import type { SVGProps } from "react";
import { AttachmentIcon } from "@/components/issue/issue-icons";
import { AgentCursorGlyph, AgentWriteGlyph } from "@/components/ui/agent-glyph";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

export function AgentPointerIcon({ size = 16, ...props }: IconProps) {
  return <AgentCursorGlyph size={size} {...props} />;
}
export function AgentSkillsIcon({ size = 14, ...props }: IconProps) {
  return (
    <svg
      {...props}
      width={size}
      height={size}
      viewBox="0 0 16 16"
      aria-hidden="true"
      fill="currentColor"
    >
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M7.46289 1.41565C8.13289 1.0953 8.9178 1.12565 9.56055 1.49768L12.877 3.41858C13.5717 3.82087 14 4.563 14 5.36584V10.5377C13.9999 11.4174 13.4872 12.2171 12.6875 12.5836L8.28809 14.5992C7.60756 14.9111 6.81653 14.8645 6.17773 14.4742L3.07715 12.5797C2.40853 12.1711 2.00025 11.4433 2 10.6598V5.44592C2.00001 4.57954 2.49776 3.79051 3.2793 3.41662L7.46289 1.41565ZM8 11.2311V13.0817L12.0625 11.2203C12.329 11.0982 12.4999 10.8309 12.5 10.5377V9.16858L8 11.2311ZM3.5 10.6598C3.50024 10.9192 3.63511 11.1599 3.85547 11.2965L6.5 12.9127V11.1705L3.5 9.33654V10.6598ZM8 7.73108V9.58166L12.5 7.51916V5.66858L8 7.73108ZM3.5 7.57971L6.5 9.41369V7.67053L3.5 5.83654V7.57971ZM8.80859 2.79651C8.59435 2.67256 8.33268 2.66139 8.10938 2.76819L4.31934 4.58069L7.2998 6.40197L11.6182 4.42248L8.80859 2.79651Z"
      />
    </svg>
  );
}
export function AgentChevronDownIcon({ ...props }: IconProps) {
  return (
    <svg
      {...props}
      width="13"
      height="9"
      viewBox="0 0 13 9"
      aria-hidden="true"
      fill="currentColor"
    >
      <path
        d="M10.1611 0.314094L5.99463 4.48054L1.82819 0.314094C1.4094 -0.104698 0.732886 -0.104698 0.314094 0.314094C-0.104698 0.732886 -0.104698 1.4094 0.314094 1.82819L5.24295 6.75705C5.66175 7.17584 6.33825 7.17584 6.75705 6.75705L11.6859 1.82819C12.1047 1.4094 12.1047 0.732886 11.6859 0.314094C11.2671 -0.0939598 10.5799 -0.104698 10.1611 0.314094Z"
        transform="translate(0.77832 0.998535)"
      />
    </svg>
  );
}
export function AgentAttachIcon({ size = 16, ...props }: IconProps) {
  return <AttachmentIcon size={size} {...props} />;
}
export function AgentSubmitIcon({ size = 16, ...props }: IconProps) {
  return (
    <svg
      {...props}
      width={size}
      height={size}
      viewBox="0 0 16 16"
      aria-hidden="true"
      fill="currentColor"
    >
      <path d="M11.48 5.674a.75.75 0 1 1-.96 1.152L8.75 5.351v6.899a.75.75 0 0 1-1.5 0V5.351L5.48 6.826a.75.75 0 0 1-.96-1.152l3-2.5a.75.75 0 0 1 .96 0l3 2.5Z" />
    </svg>
  );
}

/** Rounded square shown in the send button while the agent works (stop). */
export function AgentStopIcon({ size = 16, ...props }: IconProps) {
  return (
    <svg
      {...props}
      width={size}
      height={size}
      viewBox="0 0 16 16"
      aria-hidden="true"
      fill="currentColor"
    >
      {/* Linear's "Stop responding" glyph: an 8px rounded square. */}
      <path d="M4 7.2C4 6.07989 4 5.51984 4.21799 5.09202C4.40973 4.71569 4.71569 4.40973 5.09202 4.21799C5.51984 4 6.0799 4 7.2 4H8.8C9.92011 4 10.4802 4 10.908 4.21799C11.2843 4.40973 11.5903 4.71569 11.782 5.09202C12 5.51984 12 6.0799 12 7.2V8.8C12 9.92011 12 10.4802 11.782 10.908C11.5903 11.2843 11.2843 11.5903 10.908 11.782C10.4802 12 9.92011 12 8.8 12H7.2C6.07989 12 5.51984 12 5.09202 11.782C4.71569 11.5903 4.40973 11.2843 4.21799 10.908C4 10.4802 4 9.92011 4 8.8V7.2Z" />
    </svg>
  );
}

export function AgentWriteIcon({ size = 16, ...props }: IconProps) {
  return <AgentWriteGlyph size={size} {...props} />;
}
