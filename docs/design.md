# Deployment and recovery

## Deploy

Edit `deploy/config.yaml` for one EP group and set the namespace in
`deploy/kustomization.yaml`. Build and push an image, then set that image in
`deploy/manager.yaml`. The namespace must already exist.

```sh
make image IMAGE=your-registry/llm-d-resiliency-manager:dev
docker push your-registry/llm-d-resiliency-manager:dev
kubectl --kubeconfig=/path/to/kubeconfig --context=your-context apply -k deploy
```

Observation only lists Pods and reads engine status. Discovery maps the LWS worker
index to consecutive ranks and frontend ports; see [the example](../config/example.yaml).

## Enable recovery

Provide the [routing adapter](routing.md), then set `enableRecovery: true`, `model`
and `routingURL`. Apply `deploy/active-rbac.yaml` in the same namespace, adjusting
its ServiceAccount namespace and `resiliency-<group>` resource names to match.
Active mode uses a Lease and a ConfigMap journal. It does not write Pods or LWS.
Use one manager configuration per group; selectors must not overlap.

Optional `engineTokenFile` and `routingTokenFile` read mounted bearer tokens at
startup. Engine endpoints use Pod HTTP addresses; restrict administrative access.
The routing endpoint also supports HTTPS with system trust roots.

## Recovery behavior

After observing a healthy group, the manager requires repeated failure reports
from at least two peers and all survivors at the FT barrier. A missing frontend
alone does not trigger EP removal.

It journals a shared operation ID, waits for routing quarantine, dispatches
`scale_down` to survivors, and checks inference before admitting them. Routing
must acknowledge each change. A recovery deadline, changed membership or another
survivor failure leaves the group quarantined. In-flight requests may fail.

The journal binds the operation to Pod UIDs, container IDs and endpoints. An
interrupted dispatch is never replayed automatically. Do not delete the journal
to retry against the same processes. The manager starts over only after all
original Pods have been replaced; entering a blocked state does not trigger LWS.
Switching to observation does not clear routing exclusions.

Lease election cannot fence a paused former leader inside vLLM. Pod identity
checks cannot eliminate address-reuse races or detect child process relaunches.
These cases require operator-controlled recovery.

## Engine API

The manager uses these vLLM APIs:

| Endpoint | Required behavior |
| --- | --- |
| `GET /v1/fault_tolerance/status` | Schema 1; one engine per frontend, original global rank in `id`, peer mask when unhealthy |
| `POST /v1/fault_tolerance/apply` | `scale_down` with `removed_dp_ranks` and a shared `request_id`; HTTP 202 echoes the ID |
| `POST /v1/completions` | A short completion on every survivor before routing admission |

An HTTP 202 means accepted. Completion checks establish inference progress, not
output equivalence or recovery across later EPLB rebalances.
