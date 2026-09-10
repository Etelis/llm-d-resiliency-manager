// SPDX-License-Identifier: Apache-2.0
package recovery

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

type Options struct {
	Group              string
	EnableRecovery     bool
	Confirmations      int
	VerificationChecks int
	Timeout            time.Duration
}

type Controller struct {
	Workload Workload
	Engine   Engine
	Routing  Routing
	Journal  Journal
	Options  Options
	Now      func() time.Time
}

func (c *Controller) Reconcile(ctx context.Context) (*State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := c.Journal.Load(ctx)
	if err != nil {
		return nil, err
	}
	if state != nil {
		if err := state.Validate(); err != nil {
			return state, err
		}
		if !c.Options.EnableRecovery && state.Operation != nil {
			return state, errors.New("recovery is disabled; existing operation will not be continued")
		}
	}
	snapshot, err := c.Workload.Snapshot(ctx)
	if err == nil {
		err = snapshot.Validate()
	}
	if err != nil {
		if state != nil && state.Operation != nil && state.Phase != Blocked {
			return c.block(ctx, state, "workload inventory is unavailable or incomplete")
		}
		return state, err
	}
	if state == nil {
		state = &State{Snapshot: snapshot, Phase: Observing, Candidate: -1}
	} else if state.Incarnation != snapshot.Incarnation {
		if !fullyReplaced(state.Members, snapshot.Members) {
			if state.Operation != nil && state.Phase != Blocked {
				return c.block(ctx, state, "membership changed; partial replacement is unsupported")
			}
			return state, errors.New("membership changed; waiting for LWS to replace the full group")
		}
		state = &State{Snapshot: snapshot, Phase: Observing, Candidate: -1, Version: state.Version}
	}
	if state.Phase == Blocked {
		return state, nil
	}
	if state.Phase == Dispatching {
		return c.block(ctx, state, "dispatch outcome is unknown after interruption; automatic replay is disabled")
	}
	if state.Operation != nil && state.Phase != Degraded && state.Phase != Blocking && c.now().Sub(state.Operation.StartedAt) > c.Options.Timeout {
		return c.block(ctx, state, "recovery deadline exceeded; routing remains quarantined")
	}

	switch state.Phase {
	case Observing:
		observations := c.observe(ctx, snapshot.Members, -1)
		if allHealthy(observations, -1) {
			if state.Established && state.Candidate == -1 && state.Reason == "all ranks healthy" {
				return state, nil
			}
			state.Established = true
			state.Candidate, state.Confirmations = -1, 0
			state.Reason = "all ranks healthy"
		} else if state.Established {
			candidate, ok := Candidate(observations)
			if !ok {
				state.Candidate, state.Confirmations = -1, 0
				state.Reason = "no supported, corroborated single-rank failure"
			} else {
				if state.Candidate != candidate {
					state.Candidate, state.Confirmations = candidate, 0
				}
				if state.Confirmations < c.Options.Confirmations {
					state.Confirmations++
				}
				state.Reason = fmt.Sprintf("rank %d suspected (%d observations)", candidate, state.Confirmations)
				if state.Confirmations >= c.Options.Confirmations && c.Options.EnableRecovery {
					state.Operation = &Operation{ID: rand.Text(), Removed: candidate, StartedAt: c.now()}
					state.Phase, state.Reason = Fencing, "waiting for routing quarantine"
				}
			}
		} else {
			state.Reason = "waiting to observe a complete healthy group"
		}
	case Fencing:
		ack, err := c.publish(ctx, state, 1, false)
		if err != nil || !ack {
			return state, err
		}
		candidate, ok := Candidate(c.observe(ctx, snapshot.Members, -1))
		if !ok || candidate != state.Operation.Removed {
			return c.block(ctx, state, "fault evidence changed while routing was quarantined")
		}
		current, err := c.Workload.Snapshot(ctx)
		if err != nil || current.Incarnation != state.Incarnation {
			return c.block(ctx, state, "workload changed before dispatch")
		}
		state.Phase = Dispatching
		// The engine protocol has no durable idempotency receipt. Never replay
		// an uncertain dispatch after a manager restart.
		if err := c.Journal.Save(ctx, state); err != nil {
			return state, err
		}
		errs := c.parallel(ctx, state, func(ctx context.Context, member Member) error {
			return c.Engine.ScaleDown(ctx, member, *state.Operation)
		})
		state.Phase, state.Reason = Recovering, "waiting for survivor inference"
		if errs != nil {
			state.Reason = "some dispatch responses were uncertain: " + errs.Error()
		}
	case Recovering:
		observations := c.observe(ctx, snapshot.Members, state.Operation.Removed)
		if !allHealthy(observations, state.Operation.Removed) {
			state.Verified = 0
		} else if err := c.parallel(ctx, state, c.Engine.Verify); err != nil {
			state.Verified = 0
			state.Reason = "survivor inference check failed: " + err.Error()
		} else {
			state.Verified++
			if state.Verified >= c.Options.VerificationChecks {
				state.Phase, state.Reason = Publishing, "waiting for routing to admit survivors"
			}
		}
	case Publishing:
		if !allHealthy(c.observe(ctx, snapshot.Members, state.Operation.Removed), state.Operation.Removed) {
			return c.block(ctx, state, "survivor health changed before routing admission")
		}
		ack, err := c.publish(ctx, state, 2, true)
		if err != nil || !ack {
			return state, err
		}
		state.Phase, state.Reason = Degraded, "serving with one rank excluded"
	case Degraded:
		if allHealthy(c.observe(ctx, snapshot.Members, state.Operation.Removed), state.Operation.Removed) {
			return state, nil
		}
		state.Phase, state.Reason = Blocking, "a survivor failed; additional removals are unsupported"
	case Blocking:
		ack, err := c.publish(ctx, state, 3, false)
		if err != nil || !ack {
			return state, err
		}
		state.Phase = Blocked
	default:
		return state, fmt.Errorf("unknown journal phase %q", state.Phase)
	}
	return state, c.Journal.Save(ctx, state)
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Controller) block(ctx context.Context, state *State, reason string) (*State, error) {
	state.Phase, state.Reason = Blocking, reason
	if err := c.Journal.Save(ctx, state); err != nil {
		return state, err
	}
	ack, err := c.publish(ctx, state, 3, false)
	if err != nil || !ack {
		return state, err
	}
	state.Phase = Blocked
	return state, c.Journal.Save(ctx, state)
}

func (c *Controller) observe(ctx context.Context, members []Member, excluded int) []Observation {
	observations := make([]Observation, len(members))
	var wg sync.WaitGroup
	for _, member := range members {
		if member.Rank == excluded {
			continue
		}
		wg.Go(func() {
			observation, err := c.Engine.Observe(ctx, member)
			if err != nil {
				observation = Observation{Status: "unreachable"}
			}
			observations[member.Rank] = observation
		})
	}
	wg.Wait()
	return observations
}

func (c *Controller) parallel(ctx context.Context, state *State, fn func(context.Context, Member) error) error {
	var wg sync.WaitGroup
	errs := make([]error, len(state.Members))
	for _, member := range state.Members {
		if member.Rank == state.Operation.Removed {
			continue
		}
		wg.Go(func() { errs[member.Rank] = fn(ctx, member) })
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (c *Controller) publish(ctx context.Context, state *State, revision int, admit bool) (bool, error) {
	allowed := []int{}
	if admit {
		for _, member := range state.Members {
			if member.Rank != state.Operation.Removed {
				allowed = append(allowed, member.Rank)
			}
		}
	}
	return c.Routing.Apply(ctx, RoutingUpdate{Group: c.Options.Group, Incarnation: state.Incarnation,
		OperationID: state.Operation.ID, Revision: revision, Members: state.Members, Allowed: allowed})
}

func allHealthy(observations []Observation, excluded int) bool {
	for rank, observation := range observations {
		if rank != excluded && observation.Status != "healthy" {
			return false
		}
	}
	return len(observations) > 0
}

func fullyReplaced(old, current []Member) bool {
	uids := make([]string, 0, len(old))
	for _, member := range old {
		uids = append(uids, member.PodUID)
	}
	for _, member := range current {
		if slices.Contains(uids, member.PodUID) {
			return false
		}
	}
	return len(current) > 0
}
