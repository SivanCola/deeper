/** User turns currently mounted by the transcript, addressed by durable identity. */
export interface LoadedTurnNode {
  readonly id: string;
  readonly messageId?: string;
}

export interface LoadedTurnIndex {
  readonly byMessageId: ReadonlyMap<string, string>;
}

/** Index once per mounted order, rather than scanning it for each rail entry. */
export function indexLoadedTurns(order: readonly string[], read: (key: string) => LoadedTurnNode | undefined): LoadedTurnIndex {
  const byMessageId = new Map<string, string>();
  for (const key of order) {
    const messageId = read(key)?.messageId;
    if (messageId && !byMessageId.has(messageId)) byMessageId.set(messageId, key);
  }
  return { byMessageId };
}

/** Optimistic node keys can survive settlement; the committed message ID joins them to the directory. */
export function findLoadedTurn(index: LoadedTurnIndex, messageId: string): string | undefined {
  return index.byMessageId.get(messageId);
}
