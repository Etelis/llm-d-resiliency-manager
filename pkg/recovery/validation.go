// SPDX-License-Identifier: Apache-2.0
package recovery

import (
	"fmt"
	"net/url"
)

func (s Snapshot) Validate() error {
	if s.Incarnation == "" || len(s.Members) < 3 {
		return fmt.Errorf("expected an identified group of at least three ranks")
	}
	endpoints := map[string]bool{}
	for rank, member := range s.Members {
		u, err := url.Parse(member.Endpoint)
		if member.Rank != rank || member.PodUID == "" || member.ContainerID == "" ||
			err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" || endpoints[member.Endpoint] {
			return fmt.Errorf("invalid or duplicate member at rank %d", rank)
		}
		endpoints[member.Endpoint] = true
	}
	return nil
}

func (s State) Validate() error {
	if err := s.Snapshot.Validate(); err != nil {
		return err
	}
	switch s.Phase {
	case Observing:
		if s.Operation != nil {
			return fmt.Errorf("observing state contains an operation")
		}
	case Fencing, Dispatching, Recovering, Publishing, Degraded, Blocking, Blocked:
		if s.Operation == nil || s.Operation.ID == "" || s.Operation.StartedAt.IsZero() ||
			s.Operation.Removed <= 0 || s.Operation.Removed >= len(s.Members) {
			return fmt.Errorf("invalid recovery operation in journal")
		}
	default:
		return fmt.Errorf("unknown journal phase %q", s.Phase)
	}
	return nil
}
