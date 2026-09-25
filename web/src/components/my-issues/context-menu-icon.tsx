import {
  AlarmClock,
  Archive,
  ArrowRightLeft,
  Bell,
  BellOff,
  Box,
  CalendarDays,
  CircleDashed,
  Clipboard,
  Copy,
  CornerLeftUp,
  Diamond,
  ExternalLink,
  GitBranch,
  Hash,
  Link,
  Link2,
  RefreshCw,
  SignalHigh,
  Star,
  Tag,
  Trash2,
  Triangle,
  Type,
  UserCheck,
  UserRound,
  type LucideIcon,
} from "lucide-react";

import styles from "./my-issues-list.module.css";

/** Leading icons for top-level issue context menu items, keyed by label. */
const ICONS: Record<string, LucideIcon> = {
  Status: CircleDashed,
  Priority: SignalHigh,
  Assignee: UserRound,
  "Due date": CalendarDays,
  Labels: Tag,
  Project: Box,
  Cycle: RefreshCw,
  Estimate: Triangle,
  Milestone: Diamond,
  "Assign to me": UserCheck,
  "Set parent issue…": CornerLeftUp,
  Relations: Link2,
  "Move to team…": ArrowRightLeft,
  Subscribe: Bell,
  Unsubscribe: BellOff,
  Favorite: Star,
  "Remove from favorites": Star,
  "Remind me": AlarmClock,
  "Copy branch name": GitBranch,
  "Copy as Markdown link": Link,
  "Open in new tab": ExternalLink,
  "Make a copy…": Copy,
  Archive: Archive,
  Copy: Clipboard,
  "Copy issue URL": Link,
  "Copy issue ID": Hash,
  "Copy issue title": Type,
  Delete: Trash2,
};

export function ContextMenuIcon({ label }: { label: string }) {
  const Icon = ICONS[label];
  return Icon ? <Icon className={styles.menuIcon} size={16} aria-hidden="true" /> : null;
}
