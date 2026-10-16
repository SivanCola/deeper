# Session recovery and parallel work

Session durability and workspace mutation have separate ownership boundaries.
A current SessionRef has one writer; independent sessions can run concurrently.
Read-only tools and non-overlapping file claims can proceed in parallel. Opaque
shell or MCP mutations require the workspace write lease for that operation.
Git worktrees provide an isolated checkout when work needs independent files.

## Current recovery

Committed framed batches are authoritative. Restart recovery marks interrupted
turns and closes unresolved interactions without replaying side effects. Partial
output remains inspectable; current provider projection rules keep incomplete
tool pairs out of later model requests. File rollback requires the current
checkpoint identity and conflict checks.

Fork and rewind create independent current session identities. They preserve the
source, use the current session registry, and never create or select historical
JSONL recovery copies. The application does not scan old session files, import
old heads, or migrate earlier schema/storage revisions.

## Ownership and diagnostics

Takeover uses the current writer protocol. Background jobs and subagents remain
scoped to their owning session during controller replacement and cancellation.
Workspace lock coordination uses the current protocol across overlapping roots,
path claims and case aliases; there is no old-binary lock bridge.

`sessions diagnose`, `sessions reindex`, and `doctor session <id>` operate on
current stores. Rebuilding a derived history cache does not migrate source data.
See [session ownership](SESSION_OWNERSHIP.md), [storage](session-storage-v4.md),
and [the supported baseline](NATIVE_SESSION_STORAGE.md).
