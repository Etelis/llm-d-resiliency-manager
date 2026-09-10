// SPDX-License-Identifier: Apache-2.0
package recovery

import (
	"context"
	"time"
)

type Member struct {
	Rank        int    `json:"rank"`
	PodName     string `json:"podName"`
	PodUID      string `json:"podUID"`
	ContainerID string `json:"containerID"`
	Endpoint    string `json:"endpoint"`
}

type Snapshot struct {
	Incarnation string   `json:"incarnation"`
	Members     []Member `json:"members"`
}

type Observation struct {
	Status string `json:"status"`
	Mask   []int  `json:"mask,omitempty"`
}

type Operation struct {
	ID        string    `json:"id"`
	Removed   int       `json:"removed"`
	StartedAt time.Time `json:"startedAt"`
}

type State struct {
	Snapshot
	Phase         string     `json:"phase"`
	Reason        string     `json:"reason,omitempty"`
	Established   bool       `json:"established"`
	Candidate     int        `json:"candidate"`
	Confirmations int        `json:"confirmations"`
	Verified      int        `json:"verified"`
	Operation     *Operation `json:"operation,omitempty"`
	Version       string     `json:"-"`
}

const (
	Observing   = "observing"
	Fencing     = "fencing"
	Dispatching = "dispatching"
	Recovering  = "recovering"
	Publishing  = "publishing"
	Degraded    = "degraded"
	Blocking    = "blocking"
	Blocked     = "blocked"
)

// RoutingUpdate is a complete allowlist for the observed Pod/container incarnation.
type RoutingUpdate struct {
	Group       string   `json:"group"`
	Incarnation string   `json:"incarnation"`
	OperationID string   `json:"operationID"`
	Revision    int      `json:"revision"`
	Members     []Member `json:"members"`
	Allowed     []int    `json:"allowed"`
}

type Workload interface {
	Snapshot(context.Context) (Snapshot, error)
}

type Engine interface {
	Observe(context.Context, Member) (Observation, error)
	ScaleDown(context.Context, Member, Operation) error
	Verify(context.Context, Member) error
}

type Routing interface {
	Apply(context.Context, RoutingUpdate) (acknowledged bool, err error)
}

type Journal interface {
	Load(context.Context) (*State, error)
	Save(context.Context, *State) error
}
