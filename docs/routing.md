# Routing integration

A stalled rank can report healthy. The routing adapter must enforce exclusions
independently of engine health. This repo provides the HTTP client; the llm-d EPP
adapter still needs implementation.

## Protocol

The manager sends an idempotent `PUT` to `routingURL`:

| Field | Meaning |
| --- | --- |
| `group` | Configured EP group name |
| `incarnation` | Hash of the original membership |
| `operationID` | Shared recovery ID |
| `revision` | 1: quarantine; 2: admit survivors; 3: quarantine again |
| `members` | Full original membership: `rank`, `podName`, `podUID`, `containerID`, `endpoint` |
| `allowed` | Complete rank allowlist; empty means quarantine |

Respond with HTTP 200, echoing the publication identity:

```json
{"group":"model-0","incarnation":"membership-hash","operationID":"recovery-id","revision":1,"applied":true}
```

Return `applied: false` while convergence is pending. A 202 or mismatched receipt
does not release the recovery barrier. See [RoutingUpdate](../pkg/recovery/types.go)
for the request type and [the HTTP integration test](../test/integration/recovery_test.go)
for a complete exchange.

## Acknowledgement rules

- Every router that can select these endpoints must enforce the allowlist before
  acknowledgement. New router replicas must load it before serving.
- Quarantine stops new and queued assignments to the group. Requests already
  executing in the engine may fail.
- Match Pod UID, container ID and endpoint. Rank number or IP alone is insufficient.
- Persist the latest revision; reject older revisions and conflicting duplicates.
  Exclusions survive adapter restarts and healthy engine responses.
- Retire old membership only after all its Pod UIDs are gone. A new incarnation
  must not readmit surviving old processes.

This is a proposed adapter contract, not an existing llm-d API.
