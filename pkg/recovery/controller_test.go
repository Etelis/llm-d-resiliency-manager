// SPDX-License-Identifier: Apache-2.0
package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	controller   *Controller
	snapshot     Snapshot
	observations []Observation
	journal      []byte
	ack          bool
	verifyErr    error
	saveErr      error
	workloadErr  error
	dispatchErr  error
	updates      []RoutingUpdate
	dispatched   map[int]Operation
	dispatches   int
	mu           sync.Mutex
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{ack: true, dispatched: map[int]Operation{}, snapshot: Snapshot{Incarnation: "original"}}
	for rank := range 4 {
		f.snapshot.Members = append(f.snapshot.Members, Member{Rank: rank,
			PodName: fmt.Sprint("pod-", rank/2), PodUID: fmt.Sprint("uid-", rank/2), ContainerID: "container-0",
			Endpoint: fmt.Sprintf("http://127.0.0.1:%d", 8000+rank)})
		f.observations = append(f.observations, Observation{Status: "healthy"})
	}
	f.controller = &Controller{Workload: f, Engine: f, Routing: f, Journal: f,
		Options: Options{Group: "test", EnableRecovery: true, Confirmations: 2, VerificationChecks: 2, Timeout: time.Minute}}
	f.step(t, Observing)
	return f
}

func (f *fixture) Snapshot(context.Context) (Snapshot, error) { return f.snapshot, f.workloadErr }
func (f *fixture) Observe(_ context.Context, m Member) (Observation, error) {
	return f.observations[m.Rank], nil
}
func (f *fixture) ScaleDown(_ context.Context, m Member, op Operation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatched[m.Rank] = op
	f.dispatches++
	return f.dispatchErr
}
func (f *fixture) Verify(context.Context, Member) error { return f.verifyErr }
func (f *fixture) Apply(_ context.Context, update RoutingUpdate) (bool, error) {
	f.updates = append(f.updates, update)
	return f.ack, nil
}
func (f *fixture) Load(context.Context) (*State, error) {
	if f.journal == nil {
		return nil, nil
	}
	var state State
	err := json.Unmarshal(f.journal, &state)
	return &state, err
}
func (f *fixture) Save(_ context.Context, state *State) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	var err error
	f.journal, err = json.Marshal(state)
	return err
}
func (f *fixture) step(t *testing.T, phase string) *State {
	t.Helper()
	state, err := f.controller.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != phase {
		t.Fatalf("phase = %s, want %s (%s)", state.Phase, phase, state.Reason)
	}
	return state
}
func fault() []Observation {
	return []Observation{
		{Status: "unhealthy", Mask: []int{0, 1, 0, 0}},
		{Status: "unreachable"},
		{Status: "unhealthy", Mask: []int{0, 0, 0, 0}},
		{Status: "unhealthy", Mask: []int{0, 1, 0, 0}},
	}
}
func (f *fixture) suspect(t *testing.T) {
	t.Helper()
	f.observations = fault()
	f.step(t, Observing)
	f.step(t, Fencing)
}
func (f *fixture) recover(t *testing.T) {
	t.Helper()
	f.suspect(t)
	f.step(t, Recovering)
	for rank := range f.observations {
		f.observations[rank] = Observation{Status: "healthy"}
	}
	f.step(t, Recovering)
	f.step(t, Publishing)
	f.step(t, Degraded)
}

func TestCandidateRequiresPeerEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func([]Observation)
		want   bool
	}{
		{"worker missing", func([]Observation) {}, true},
		{"stalled worker reports healthy", func(o []Observation) { o[1].Status = "healthy" }, true},
		{"frontend only", func(o []Observation) {
			for _, i := range []int{0, 2, 3} {
				o[i] = Observation{Status: "healthy"}
			}
		}, false},
		{"one witness", func(o []Observation) { o[3].Mask = []int{0, 0, 0, 0} }, false},
		{"two failed ranks", func(o []Observation) { o[0].Mask[2] = 1 }, false},
		{"survivor not at barrier", func(o []Observation) { o[2].Status = "healthy" }, false},
		{"master failed", func(o []Observation) {
			o[0], o[1] = o[1], o[0]
			o[1].Mask = []int{1, 0, 0, 0}
			o[3].Mask = []int{1, 0, 0, 0}
		}, false},
		{"invalid mask", func(o []Observation) { o[0].Mask[2] = 2 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := fault()
			tt.change(o)
			rank, ok := Candidate(o)
			if ok != tt.want || (ok && rank != 1) {
				t.Fatalf("Candidate = (%d, %v)", rank, ok)
			}
		})
	}
}

func TestRecoveryWaitsForRoutingAndInference(t *testing.T) {
	f := newFixture(t)
	f.suspect(t)
	f.ack = false
	f.step(t, Fencing)
	if len(f.dispatched) != 0 {
		t.Fatal("dispatched before routing acknowledged quarantine")
	}
	f.ack = true
	f.step(t, Recovering)
	state, _ := f.Load(context.Background())
	for _, rank := range []int{0, 2, 3} {
		if op, ok := f.dispatched[rank]; !ok || op.ID != state.Operation.ID || op.Removed != 1 {
			t.Fatalf("rank %d received %v", rank, op)
		}
	}
	if len(f.dispatched) != 3 || f.dispatches != 3 {
		t.Fatal("wrong dispatch targets or repeated command")
	}
	for rank := range f.observations {
		f.observations[rank] = Observation{Status: "healthy"}
	}
	f.verifyErr = errors.New("inference timeout")
	f.step(t, Recovering)
	f.verifyErr = nil
	f.step(t, Recovering)
	f.step(t, Publishing)
	f.step(t, Degraded)
	last := f.updates[len(f.updates)-1]
	if last.Revision != 2 || !slices.Equal(last.Allowed, []int{0, 2, 3}) {
		t.Fatalf("routing = %+v", last)
	}
}

func TestObserveModeNeverDispatches(t *testing.T) {
	f := newFixture(t)
	f.controller.Options.EnableRecovery = false
	f.observations = fault()
	for range 4 {
		f.step(t, Observing)
	}
	if len(f.dispatched)+len(f.updates) != 0 {
		t.Fatal("observe mode had external effects")
	}
}

func TestJournalFailurePreventsDispatch(t *testing.T) {
	f := newFixture(t)
	f.suspect(t)
	f.saveErr = errors.New("journal unavailable")
	if _, err := f.controller.Reconcile(context.Background()); err == nil {
		t.Fatal("expected journal error")
	}
	if len(f.dispatched) != 0 {
		t.Fatal("dispatch was not durably recorded first")
	}
}

func TestInterruptedDispatchIsNeverReplayed(t *testing.T) {
	f := newFixture(t)
	f.suspect(t)
	state, _ := f.Load(context.Background())
	state.Phase = Dispatching
	if err := f.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	f.step(t, Blocked)
	f.step(t, Blocked)
	if len(f.dispatched) != 0 {
		t.Fatal("replayed uncertain dispatch")
	}
	if last := f.updates[len(f.updates)-1]; last.Revision != 3 || len(last.Allowed) != 0 {
		t.Fatal("routing was not quarantined")
	}
}

func TestAmbiguousResponseCanFinishWithoutReplay(t *testing.T) {
	f := newFixture(t)
	f.dispatchErr = errors.New("response lost")
	f.recover(t)
	if len(f.dispatched) != 3 || f.dispatches != 3 {
		t.Fatal("wrong dispatch targets or repeated command")
	}
}

func TestAdmissionTimeoutPublishesNewQuarantine(t *testing.T) {
	f := newFixture(t)
	f.suspect(t)
	f.step(t, Recovering)
	for rank := range f.observations {
		f.observations[rank] = Observation{Status: "healthy"}
	}
	f.step(t, Recovering)
	f.step(t, Publishing)
	f.ack = false
	f.step(t, Publishing)
	f.controller.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	f.step(t, Blocking)
	f.ack = true
	f.step(t, Blocked)
	last := f.updates[len(f.updates)-1]
	if last.Revision != 3 || len(last.Allowed) != 0 {
		t.Fatal("potentially applied admission was not superseded")
	}
}

func TestMembershipChangesNeverReplayOldOperation(t *testing.T) {
	for _, change := range []string{"missing pod", "partial replacement", "container restart"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			f.recover(t)
			switch change {
			case "missing pod":
				f.workloadErr = errors.New("pod missing")
			case "partial replacement":
				f.snapshot.Incarnation = "mixed"
				f.snapshot.Members[0].PodUID = "new-uid"
			case "container restart":
				f.snapshot.Incarnation = "restarted"
				f.snapshot.Members[0].ContainerID = "container-1"
			}
			f.step(t, Blocked)
			if f.dispatches != 3 {
				t.Fatal("old operation was replayed")
			}
			last := f.updates[len(f.updates)-1]
			if len(last.Allowed) != 0 {
				t.Fatal("old membership was not quarantined")
			}
			f.workloadErr = nil
			f.snapshot.Incarnation = "replacement"
			for i := range f.snapshot.Members {
				f.snapshot.Members[i].PodUID = fmt.Sprint("new-", i/2)
			}
			state := f.step(t, Observing)
			if state.Operation != nil {
				t.Fatal("full replacement inherited the old operation")
			}
		})
	}
}

func TestSurvivorFailureDoesNotRemoveAnotherRank(t *testing.T) {
	f := newFixture(t)
	f.recover(t)
	f.observations[2].Status = "unhealthy"
	f.step(t, Blocking)
	f.step(t, Blocked)
	if f.dispatches != 3 {
		t.Fatal("second removal dispatched")
	}
}
