# Phase 2: replacement and rejoin

Phase 1 ends with a smaller serving group. Phase 2 should restore its capacity by
replacing the failed worker and joining it to the surviving group. We intend to
keep that work in this repository and retain one coordinator per recovery operation.

## Required lifecycle support

The LWS work must first define a supported way to replace a failed member without
replacing the survivors. It also needs a clear owner for replacement, an observable
generation, and a fallback to full-group reset if recovery cannot complete.
Keeping `restartPolicy: None` indefinitely or racing LWS with ownership patches is
not the intended integration.

The engine needs more than an HTTP acceptance response: a capability check,
operation status, durable idempotency, and a membership epoch that rejects stale
controllers. Rejoin also needs a protocol for initializing the new process,
restoring expert placement and communication state, and committing the new group.
Weights, KV state, CUDA graphs and collective resources have different lifetimes;
we should make their recovery guarantees explicit for each backend.

## Intended flow

| Step | Owner | Completion evidence |
| --- | --- | --- |
| Exclude failed member | Manager, routing, engine | Acknowledged routing exclusion and serving survivors |
| Replace member | LWS through its supported lifecycle API | New Pod UID and expected generation |
| Prepare replacement | Engine/runtime | Model and communication resources initialized |
| Join group | Engine, coordinated by manager | All members acknowledge the new membership epoch |
| Verify and admit | Manager and routing | Inference checks pass; routing acknowledges admission |

The manager should persist each step, deadline and receipt. A restart should resume
from observed state without repeating an uncertain side effect. A failed join must
either leave the old survivor group usable or trigger the agreed full reset; a
partially committed group must not serve traffic.

## Repository changes when the contracts are ready

Extend the workload adapter for replacement status and requests, the engine adapter
for prepare/join/commit, and the journal for membership generations and receipts.
Keep routing admission in the existing state machine. Add a CRD if operation status
needs to be consumed by other controllers or multiple groups need reconciliation;
we do not need a speculative API or a separate service for the first version.

Acceptance requires worker and node replacement, controller restart at every
transition, stale leader rejection, repeated recovery, rollback, and sustained
inference across EPLB rebalances. GPU/backend coverage and output checks must be
recorded for each supported configuration. Phase 1 measurements do not establish
any of these guarantees.
