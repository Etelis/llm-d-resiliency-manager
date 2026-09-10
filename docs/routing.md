# Routing acknowledgement contract

An excluded rank can keep returning HTTP 200 from its health endpoint. A routing
adapter must enforce the manager's membership decision independently of engine
health. This repository contains the HTTP client and tests; an llm-d EPP adapter
is still needed before active end-to-end recovery can be deployed.

The configured `routingURL` accepts an idempotent `PUT` with this shape:

```json
{
  "group": "model-0",
  "incarnation": "hash-of-original-members",
  "operationID": "shared-recovery-id",
  "revision": 1,
  "members": [
    {"rank": 0, "podName": "model-0-0", "podUID": "uid-a", "containerID": "container-a", "endpoint": "http://10.0.0.1:8000"},
    {"rank": 1, "podName": "model-0-1", "podUID": "uid-b", "containerID": "container-b", "endpoint": "http://10.0.0.2:8000"},
    {"rank": 2, "podName": "model-0-2", "podUID": "uid-c", "containerID": "container-c", "endpoint": "http://10.0.0.3:8000"}
  ],
  "allowed": []
}
```

`members` always contains the full original cohort. `allowed` is its complete
rank allowlist, not a patch. Revision 1 quarantines the cohort; revision 2 admits
only the verified survivors; revision 3 quarantines it again if recovery becomes
uncertain or another survivor fails.

The response must be HTTP 200 and echo the exact publication:

```json
{
  "group": "model-0",
  "incarnation": "hash-of-original-members",
  "operationID": "shared-recovery-id",
  "revision": 1,
  "applied": true
}
```

Return `applied: false` while convergence is pending. A 202, a mismatched receipt,
or an acknowledgement of a ConfigMap write alone does not release the recovery
barrier. The controller retries publications, including after a restart.

## Adapter requirements

- Acknowledge only after every routing instance that can select these endpoints
  enforces the allowlist. New routing replicas must load it before serving.
- Quarantine must stop new assignments and retire queued assignments to the
  affected cohort before acknowledgement. It does not promise to recover requests
  already executing inside the engine.
- Resolve targets by Pod UID, container ID and endpoint. Rank number or IP alone
  is insufficient when Pods restart or addresses are reused.
- Persist the latest revision. Reject lower revisions and conflicting payloads
  for the same revision. A delayed revision 2 must not undo revision 3.
- Preserve exclusions across adapter restarts and engine health changes. An
  excluded rank's healthy response must never readmit it.
- A new incarnation must not relax exclusions for any surviving old process.
  Retire old publications only after Kubernetes confirms all their Pod UIDs are
  gone; a completely replaced group can return to normal discovery and health policy.

These semantics are a proposed integration contract, not an existing llm-d API.
The HTTP simulation tests the client and sequencing. It cannot establish that
real routers, queued requests or endpoint caches satisfy this contract.
