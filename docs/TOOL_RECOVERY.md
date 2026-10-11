# Tool interruption and recovery

Deepzora records execution facts so every provider tool call has an honest result. These facts describe uncertainty; they do not grant or revoke access to later tools.

## Outcome model

| Outcome | Meaning | Later behavior |
| --- | --- | --- |
| `not_started` | The durable start boundary was never crossed. | The model may issue a new call. |
| completed or failed | The host received a reliable terminal result. | The result and execution metadata are retained. |
| interrupted after start | Cancellation happened after the tool body began. Side effects may exist. | The model is advised to inspect state. Tools remain available. |
| `unknown` | Recovery found a durable start with no reliable terminal result. | A paired unknown result is added once. Tools remain available. |

A call-start record is persisted before entering the tool body. Cancellation stops process trees and waits for started calls to settle; calls that never start receive an explicit result. Stopping a local process does not prove that a remote side effect did not occur.

Recovery never automatically replays a call, synthesizes success from current state, blocks the same arguments, or installs a global read-only barrier. The reconstructed context lists which calls did not start, completed, or remain unknown. It advises retrying read-only or idempotent work as needed and checking external state before repeating a side effect. Each unknown record is added once to durable context rather than dynamically changing the system prompt every round.

## Recovery facts

Desktop `GetToolRecoveryForTab` and Serve `GET /tool-recovery` expose a session-scoped snapshot of current unknown execution facts. Recovery is model-directed; there are no host confirm, reject, replay, or resume action endpoints. Retired Auto Guard configuration and sidecars are unsupported.

There is no host-level exactly-once guarantee for an external service that offers no authoritative receipt or idempotency mechanism. The model and user must inspect that service before repeating an uncertain side effect.
