import type { SVGProps } from "react";
import { AttachmentIcon } from "@/components/issue/issue-icons";
import { AgentCursorGlyph, AgentWriteGlyph } from "@/components/ui/agent-glyph";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

export function AgentWatermarkIcon(props: IconProps) {
  return <svg {...props} viewBox="0 0 336 336" aria-hidden="true" style={{ fill: 'var(--flow-logo-outline-fill)', stroke: 'var(--flow-logo-outline-stroke)' }} strokeWidth="0.5">
    <path d="M6.06836 206.256C5.32965 203.107 9.0812 201.123 11.3683 203.41L132.59 324.632C134.877 326.919 132.893 330.67 129.744 329.932C68.5709 315.581 20.4187 267.429 6.06836 206.256Z" />
    <path d="M2.00628 157.672C1.9477 158.612 2.30133 159.531 2.96766 160.197L175.803 333.032C176.469 333.699 177.388 334.052 178.328 333.994C186.194 333.504 193.912 332.467 201.443 330.92C203.981 330.399 204.863 327.28 203.031 325.448L10.5521 132.969C8.71999 131.137 5.60148 132.019 5.08022 134.557C3.53338 142.088 2.49619 149.806 2.00628 157.672Z" />
    <path d="M15.9803 100.622C15.4276 101.863 15.7091 103.313 16.6697 104.274L231.726 319.33C232.687 320.291 234.137 320.572 235.378 320.02C241.308 317.378 247.055 314.4 252.594 311.109C254.427 310.02 254.71 307.501 253.202 305.994L30.0064 82.7979C28.4988 81.2903 25.9802 81.5732 24.8911 83.4061C21.6 88.9453 18.6216 94.692 15.9803 100.622Z" />
    <path d="M44.027 62.0058C42.7982 60.777 42.722 58.806 43.8797 57.51C74.3081 23.4449 118.57 2 167.84 2C259.608 2 334 76.3922 334 168.16C334 217.43 312.555 261.692 278.49 292.12C277.194 293.278 275.223 293.202 273.994 291.973L44.027 62.0058Z" />
  </svg>;
}

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
      {/* The reference app's "Stop responding" glyph: an 8px rounded square. */}
      <path d="M4 7.2C4 6.07989 4 5.51984 4.21799 5.09202C4.40973 4.71569 4.71569 4.40973 5.09202 4.21799C5.51984 4 6.0799 4 7.2 4H8.8C9.92011 4 10.4802 4 10.908 4.21799C11.2843 4.40973 11.5903 4.71569 11.782 5.09202C12 5.51984 12 6.0799 12 7.2V8.8C12 9.92011 12 10.4802 11.782 10.908C11.5903 11.2843 11.2843 11.5903 10.908 11.782C10.4802 12 9.92011 12 8.8 12H7.2C6.07989 12 5.51984 12 5.09202 11.782C4.71569 11.5903 4.40973 11.2843 4.21799 10.908C4 10.4802 4 9.92011 4 8.8V7.2Z" />
    </svg>
  );
}

export function AgentWriteIcon({ size = 16, ...props }: IconProps) {
  return <AgentWriteGlyph size={size} {...props} />;
}
/** The reference app's "selected text" chip icon: an "A" in an open box. */
export function AgentSelectedTextIcon({ size = 13, ...props }: IconProps) {
  return (
    <svg {...props} width={size} height={size} viewBox="0 0 16 16" aria-hidden="true" fill="currentColor">
      <path d="M1 2.25C1 1.56 1.56 1 2.25 1h11.5c.69 0 1.25.56 1.25 1.25V5.5h-1.5v-3h-11v3H1zM13.5 10v3.5h-11V10H1v3.75c0 .69.56 1.25 1.25 1.25h11.5c.69 0 1.25-.56 1.25-1.25V10z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M5.944 12a.4.4 0 0 0 .386-.295l.456-1.68h2.43l.458 1.68a.4.4 0 0 0 .386.295h.41a.4.4 0 0 0 .385-.51l-2.047-7.2A.4.4 0 0 0 8.423 4H7.58a.4.4 0 0 0-.384.29l-2.052 7.2a.4.4 0 0 0 .385.51zm2.958-3.136-.876-3.221h-.052l-.873 3.221z" />
    </svg>
  );
}
/** The reference app's "Use … as context" toggle: four corner brackets. */
export function AgentPageContextIcon({ size = 14, ...props }: IconProps) {
  return (
    <svg {...props} width={size} height={size} viewBox="0 0 16 16" aria-hidden="true" fill="currentColor">
      <path fillRule="evenodd" clipRule="evenodd" d="M1 4.75V4.25C1 2.45507 2.45507 1 4.25 1H4.75C5.16421 1 5.5 1.33579 5.5 1.75C5.5 2.16421 5.16421 2.5 4.75 2.5H4.25C3.2835 2.5 2.5 3.2835 2.5 4.25V4.75C2.5 5.16421 2.16421 5.5 1.75 5.5C1.33579 5.5 1 5.16421 1 4.75Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M11.25 1L11.75 1C13.5449 1 15 2.45507 15 4.25V4.75L14.9961 4.82715C14.9575 5.20512 14.6382 5.5 14.25 5.5C13.8618 5.5 13.5425 5.20512 13.5039 4.82715L13.5 4.75V4.25C13.5 3.2835 12.7165 2.5 11.75 2.5H11.25C10.8358 2.5 10.5 2.16421 10.5 1.75C10.5 1.33579 10.8358 1 11.25 1Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M4.75 14.9951H4.25C2.45507 14.9951 1 13.54 1 11.7451L1 11.2451L1.00391 11.168C1.04253 10.79 1.36183 10.4951 1.75 10.4951C2.13817 10.4951 2.45747 10.79 2.49609 11.168L2.5 11.2451V11.7451C2.5 12.7116 3.2835 13.4951 4.25 13.4951H4.75C5.16421 13.4951 5.5 13.8309 5.5 14.2451C5.5 14.6593 5.16421 14.9951 4.75 14.9951Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M11.25 15H11.75C13.5449 15 15 13.5449 15 11.75V11.25L14.9961 11.1729C14.9575 10.7949 14.6382 10.5 14.25 10.5C13.8618 10.5 13.5425 10.7949 13.5039 11.1729L13.5 11.25V11.75C13.5 12.7165 12.7165 13.5 11.75 13.5H11.25C10.8358 13.5 10.5 13.8358 10.5 14.25C10.5 14.6642 10.8358 15 11.25 15Z" />
    </svg>
  );
}
