# Session experience

The desktop Settings page uses one **Session experience** preference to control how work in progress is presented in a transcript.

## Modes

| Mode | While a turn is running | After the turn completes |
| --- | --- | --- |
| **Standard** | Work in progress is visible. | Completed work is collapsed by default; expand it from the message when needed. |
| **Deep** | The complete work process is shown in real time. | Completed work remains expanded; individual sections can still be collapsed manually. |

This preference applies to reasoning, tool calls, sub-task progress, work-process cards, approvals, validation, and the active turn. It changes presentation only. It does not change the selected model, reasoning strength, provider request, cost, context window, or saved transcript data.

Manual expand/collapse is a message-level reading action and is retained for the message row. It does not create another global setting.
Overrides are keyed by session and stable process-segment identity in a bounded in-memory cache. They survive React re-renders and transcript window recycling, but intentionally do not persist across application restarts.

Warnings, approvals, readiness states, extension cards, and other items that require user action remain outside the completed work-process fold, so standard execution never makes an action unreachable.

## Configuration

The canonical desktop configuration is:

```toml
[desktop]
session_experience = "standard"
```

The valid values are `standard` and `deep`; missing or invalid values use `standard`. Go Settings and startup snapshots are authoritative. Local storage cannot override them. Retired display fields and setters are unsupported.
