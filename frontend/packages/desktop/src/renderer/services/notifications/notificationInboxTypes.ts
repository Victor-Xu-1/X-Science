export type NotificationKind = 'task.completed' | 'task.failed' | 'task.needs-attention' | 'authorization.required';
export type InboxNotification = Readonly<{
  id: string;
  originId: string;
  kind: NotificationKind;
  conversationId: string;
  frameIncarnationId: string;
  title: string;
  createdAt: string;
  readAt: string | null;
  actionState: 'pending' | 'resolved' | null;
  targetInvalidatedAt: string | null;
}>;
export type InboxSnapshot = Readonly<{
  historyScope: 'active-transcript-sources';
  revision: number;
  unreadCount: number;
  items: readonly InboxNotification[];
  readReceipt: string;
  incomplete: boolean;
  nextCursor: string | null;
  incompleteReason: 'source_unavailable' | 'capacity' | 'more_sources' | null;
  historyTruncated: boolean;
}>;
export type InboxMutation =
  | Readonly<{ kind: 'read'; ids: readonly string[] }>
  | Readonly<{ kind: 'read-all'; receipt: string }>;
export type InboxView = {
  account: string;
  enabled: boolean;
  data: InboxSnapshot | null;
  status: 'idle' | 'loading' | 'ready' | 'error';
  readError: boolean;
  writeStatus: 'idle' | 'pending' | 'uncertain';
  writeError: boolean;
  targetError: boolean;
  openingTarget: string | null;
  openBellId: string | null;
};
