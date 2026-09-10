// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Etelis/llm-d-resiliency-manager/internal/engine"
	"github.com/Etelis/llm-d-resiliency-manager/internal/httpjson"
	kube "github.com/Etelis/llm-d-resiliency-manager/internal/kubernetes"
	"github.com/Etelis/llm-d-resiliency-manager/internal/routing"
	"github.com/Etelis/llm-d-resiliency-manager/pkg/recovery"
)

type snapshotFunc func(context.Context) (recovery.Snapshot, error)

func (f snapshotFunc) Snapshot(ctx context.Context) (recovery.Snapshot, error) { return f(ctx) }

func TestHTTPRecoveryKeepsStalledRankExcluded(t *testing.T) {
	var mu sync.Mutex
	faulted, recovered, inferenceWorks := false, false, false
	staleAck := true
	var requests []string
	var allowed []int
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPut {
			t.Error("routing publication must use PUT")
		}
		var update recovery.RoutingUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			t.Error(err)
			return
		}
		allowed = slices.Clone(update.Allowed)
		revision := update.Revision
		if staleAck {
			revision--
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"group": update.Group,
			"incarnation": update.Incarnation, "operationID": update.OperationID, "revision": revision, "applied": true})
	}))
	defer router.Close()
	snapshot := recovery.Snapshot{Incarnation: "http-test"}
	for rank := range 4 {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			switch r.URL.Path {
			case "/v1/fault_tolerance/status":
				status := "healthy"
				mask := []int{0, 0, 0, 0}
				if faulted && !recovered && rank != 1 {
					status = "unhealthy"
					mask[1] = 1
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "total_engines": 1,
					"engines": []any{map[string]any{"id": rank, "status": status, "mask": mask}}})
			case "/v1/fault_tolerance/apply":
				if rank == 1 || r.Method != http.MethodPost || len(allowed) != 0 {
					t.Error("unsafe dispatch")
				}
				var body struct {
					Instruction string `json:"instruction"`
					RequestID   string `json:"request_id"`
					Params      struct {
						Removed []int `json:"removed_dp_ranks"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.Instruction != "scale_down" || body.RequestID == "" || !slices.Equal(body.Params.Removed, []int{1}) {
					t.Error("invalid scale_down payload")
				}
				requests = append(requests, body.RequestID)
				if len(requests) == 3 {
					recovered = true
				}
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]string{"request_id": body.RequestID})
			case "/v1/completions":
				if rank == 1 || !inferenceWorks {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]string{"text": "ready", "finish_reason": "stop"}}})
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		snapshot.Members = append(snapshot.Members, recovery.Member{Rank: rank, PodUID: fmt.Sprint(rank), ContainerID: "running", Endpoint: server.URL})
	}
	client, err := httpjson.New(time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	c := recovery.Controller{Workload: snapshotFunc(func(context.Context) (recovery.Snapshot, error) { return snapshot, nil }),
		Engine: &engine.VLLM{Client: client, Model: "test-model"}, Routing: &routing.HTTP{Client: client, Endpoint: router.URL},
		Journal: &kube.MemoryJournal{}, Options: recovery.Options{Group: "test", EnableRecovery: true, Confirmations: 2, VerificationChecks: 2, Timeout: time.Minute}}
	step := func(want string) {
		t.Helper()
		state, err := c.Reconcile(context.Background())
		if err != nil || state.Phase != want {
			t.Fatalf("phase: %v, error: %v; want %s", state, err, want)
		}
	}
	step(recovery.Observing)
	mu.Lock()
	faulted = true
	mu.Unlock()
	step(recovery.Observing)
	step(recovery.Fencing)
	if _, err := c.Reconcile(context.Background()); err == nil {
		t.Fatal("stale routing acknowledgement accepted")
	}
	mu.Lock()
	if len(requests) != 0 {
		t.Fatal("dispatch preceded routing acknowledgement")
	}
	staleAck = false
	mu.Unlock()
	step(recovery.Recovering)
	step(recovery.Recovering)
	mu.Lock()
	inferenceWorks = true
	mu.Unlock()
	step(recovery.Recovering)
	step(recovery.Publishing)
	step(recovery.Degraded)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(allowed, []int{0, 2, 3}) {
		t.Fatalf("allowed ranks = %v", allowed)
	}
	if len(requests) != 3 || requests[0] != requests[1] || requests[1] != requests[2] {
		t.Fatalf("recovery IDs = %v", requests)
	}
}
