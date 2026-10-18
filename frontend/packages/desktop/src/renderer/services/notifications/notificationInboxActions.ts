import { verifyNotificationTarget, writeNotificationRead } from './notificationInboxService';
import type { NotificationInboxStore } from './NotificationInboxStore';
import type { InboxMutation } from './notificationInboxTypes';

async function sendRead(store: NotificationInboxStore): Promise<boolean> {
  const command = store.command;
  if (!command || store.writePromise || !store.snapshot.enabled) return false;
  const account = store.snapshot.account,
    controller = store.requestController();
  store.publish({ writeStatus: 'pending', writeError: false });
  let succeeded = false;
  const operation = (async () => {
    try {
      const snapshot = await writeNotificationRead(command, { signal: controller.signal });
      if (!store.current(account)) return;
      store.receive(snapshot);
      store.command = null;
      succeeded = true;
      store.publish({ writeStatus: 'idle', writeError: false });
    } catch {
      if (store.current(account)) store.publish({ writeStatus: 'uncertain', writeError: true });
    } finally {
      store.releaseRequest(controller);
      if (store.current(account) && store.writePromise === operation) {
        store.writePromise = null;
        store.settleInvalidation();
      }
    }
  })();
  store.writePromise = operation;
  await operation;
  return succeeded;
}
export function startInboxMutation(store: NotificationInboxStore, command: InboxMutation): Promise<boolean> {
  if (!store.current() || !store.snapshot.enabled || store.command || store.writePromise) return Promise.resolve(false);
  if (
    command.kind === 'read' &&
    (command.ids.length === 0 || command.ids.some((id) => !store.snapshot.data?.items.some((item) => item.id === id)))
  )
    return Promise.resolve(false);
  store.command = command;
  return sendRead(store);
}
export function retryInboxMutation(store: NotificationInboxStore): Promise<boolean> {
  if (!store.current() || store.snapshot.writeStatus !== 'uncertain') return Promise.resolve(false);
  return sendRead(store);
}
export async function openInboxTarget(
  store: NotificationInboxStore,
  id: string,
  navigate: (path: string) => void | Promise<void>
): Promise<boolean> {
  const item = store.snapshot.data?.items.find((candidate) => candidate.id === id);
  if (
    !item ||
    item.targetInvalidatedAt ||
    store.snapshot.openingTarget ||
    !store.snapshot.openBellId ||
    !store.current()
  )
    return false;
  const account = store.snapshot.account,
    opening = store.openingVersion,
    controller = store.requestController();
  store.publish({ openingTarget: id, targetError: false });
  try {
    const path = await verifyNotificationTarget(item, { signal: controller.signal });
    const current = store.snapshot.data?.items.find((candidate) => candidate.id === id);
    if (
      !store.current(account) ||
      store.openingVersion !== opening ||
      !current ||
      current.targetInvalidatedAt ||
      current.conversationId !== item.conversationId ||
      current.frameIncarnationId !== item.frameIncarnationId
    )
      return false;
    await navigate(path);
    if (!store.current(account) || store.openingVersion !== opening) return false;
    const read = current.readAt === null ? await store.markRead([id]) : true;
    return read && store.current(account) && store.openingVersion === opening;
  } catch {
    if (store.current(account) && store.openingVersion === opening) store.publish({ targetError: true });
    return false;
  } finally {
    store.releaseRequest(controller);
    if (store.current(account)) store.publish({ openingTarget: null });
  }
}
