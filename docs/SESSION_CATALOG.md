# Current session metadata and Desktop startup

Current framed session storage is authoritative for session identity, immutable workspace headers, conversation events and titles. The workspace registry owns membership, presentation and lifecycle. Desktop lists use these two current owners directly. There is no separate `internal/sessioncatalog` SQLite database, legacy directory scanner or metadata-sidecar migration.

## Reads and status

- Project shells read registry metadata. Collapsed folders read only their current pinned members. Session pages use immutable read snapshots with current membership/lifecycle fences.
- `Query.Stat` and `Query.List` read current metadata and overlay an attached runtime. Missing or damaged disposable metadata can enter the existing bounded repair queue. A status request itself never scans directories or replays history.
- `Query.CatalogState` reports observed pending and failed metadata, active background workers and a monotonic revision. A coalesced change channel wakes the owning Desktop observer. Repeating an unchanged observation does not publish another revision.
- Desktop total counts come from current registered sessions, excluding deleted identities. Health describes observed metadata problems; it is not a certification that every event in an unvisited session has been checked.
- Open/running state remains a live runtime projection. Current titles and creation times are never overridden by old topic indexes.

## Explicit repair

`RebuildSessionCatalog` refreshes metadata for current registered sessions through `Query.RefreshMetadata`. It reduces their current events into `.query-cache/<session-id>/catalog-metadata.json`; it does not rewrite manifests, event frames, attachments or registry membership. Unknown old JSONL files are not inputs.

One host-owned flight handles a rebuild. Concurrent requests join its result. Individual failures do not prevent attempts on the other registered sessions; failed metadata remains visible in status and can be retried. Publication is fenced against deleted/replaced session generations. The host cancels repair on shutdown; a timed-out stop retains the outstanding join until the worker releases its resources.

The sidebar reports current pending, active and failed repair states. It has no old database-mode fallback, quarantine path, deferred legacy-repair timestamp or aggregate-only backend compatibility branch.

## Other indexes

History search, task and usage catalogs retain their separate current responsibilities. Desktop startup registers current query roots for history search and current project roots for task queries. CLI `sessions reindex` repairs current session metadata; `doctor catalogs` no longer advertises the removed session SQLite projection.

## Validation

Current regressions cover cache damage and retry, concurrent rebuild joining, cancellation with outstanding work, unchanged source bytes, current pinned/session pagination and cross-project identity. Native workspace watching retains its independent descriptor-budget regression; the removed legacy session directory watcher has no runtime or test substitute.
