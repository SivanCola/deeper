# Independent sessions

[简体中文](INDEPENDENT_SESSIONS.zh-CN.md)

Every Desktop session is identified by HostID and SessionID. Its row, selection, unread state, title, menu, runtime, and notifications resolve to that same identity. Topic association does not make sibling sessions equal. Subagents and automatic recovery copies retain their own semantics.

Forks retain ParentSessionID, inherit the parent's group, default to unpinned, and follow the parent when manual ordering is enabled. An activity-sorted workspace stays activity-sorted. Fork persistence and publication are recoverable: retries attach the same durable child without resetting later title, pin, or group edits.

Requests capture session identity and generation. Invalid references fail; local resolvers reject remote references. Remote writes use the remote owner and negotiated identifiers, with no topic-wide fallback. Activation tickets retain the original navigation intent so a delayed open cannot overtake a newer click.

The current workspace registry owns membership and organization: revision, explicit order, manual-order preference, and groups. Pins belong to session presentation. `GetSessionOrganization` returns a snapshot; `UpdateSessionOrganization` applies semantic mutations with an expected revision. A visible partial page is never submitted as the entire order. Conflicts return the latest snapshot; the client retries the original mutation at most twice, then reloads with an error.

Only the current registry schema is supported. There is no import from old topic sidecars, source adoption journal, or downgraded-writer mirror. Unknown schema versions are rejected without overwriting their bytes. Workspace assignment is separate from transcript storage.

Lists normalize identities, deduplicate, filter and sort before publishing an immutable result. Lifecycle, organization and presentation share the same membership projection. Session headers and aggregates come from bounded store/index queries; transcript bodies are loaded only through the history owner. Late requests and callbacks cannot update a replacement session.

See [Desktop architecture](DESKTOP_ARCHITECTURE.md), [current baseline](current-version-baseline.md), and [history/scroll contract](TRANSCRIPT_SCROLL_CONTRACT.md).
