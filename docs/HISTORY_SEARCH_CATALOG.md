# History Search Catalog

Deepzora stores the history search projection at
`<cache root>/history-search/v1.sqlite`. Current main and child session catalogs
provide the authoritative framed events. Removing or rebuilding the search
database never removes conversation data. Every index root requires a current
session query; paths alone cannot grant read access, and old JSONL files are
not scanned or imported.

The catalog stores normalized retrieval tokens in FTS5, not complete message
text. English tokens retain the existing lowercase/code-symbol semantics and
CJK text retains overlapping bigrams. Snippets and `around` context are loaded
from the authoritative source only after SQLite has selected the final
candidates. User, assistant, tool input, tool error, and tool output parts are
indexed; ordinary tool output remains excluded by the default search kinds.

Indexing lists current identities and compares their content digest and metadata
against the persisted projection. Missing revisions enqueue a coalesced source
refresh. Changed sources replace their indexed tokens from a consistent current
snapshot; no display-index offsets or branch sidecars are read. Full reconciles,
queued refreshes, removals and capacity maintenance share one writer order.
This prevents overlapping discovery generations from hiding present sessions.
A queued content update preserves its source's discovery generation.

The Agent `history` tool and Desktop history manager share the projection.
Search never starts a synchronous directory scan. While indexing is incomplete,
existing results return immediately with explicit progress. Runtime open,
running, and current state is overlaid from memory; SQLite cannot restore stale
runtime ownership. Provider-visible tool name, description, schema, defaults,
and ordering are unchanged.

The database uses the common disposable projection policy: local disks use WAL,
`synchronous=NORMAL`, foreign keys, private permissions, and a short busy
timeout. Remote or unavailable cache directories fall back to memory. Integrity
or migration failures quarantine and rebuild the cache; future schema versions
are preserved in degraded read mode.

Diagnostics and safe rebuild commands:

```sh
deepzora doctor catalogs [--json]
deepzora catalogs reindex history [--dir PATH ...] [--json]
```

Diagnostics never print queries, tokens, snippets, messages, tool arguments, or
provider content. A rebuild replaces only this disposable projection.
