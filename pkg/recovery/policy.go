// SPDX-License-Identifier: Apache-2.0
package recovery

// Candidate requires corroborated peer masks and all survivors at the FT barrier.
// A missing frontend alone is not evidence that its EP worker has stopped.
func Candidate(observations []Observation) (int, bool) {
	votes := make([]int, len(observations))
	for rank, observation := range observations {
		if observation.Status != "unhealthy" || len(observation.Mask) != len(observations) {
			continue
		}
		for peer, masked := range observation.Mask {
			if masked != 0 && masked != 1 {
				return 0, false
			}
			if masked == 1 && peer != rank {
				votes[peer]++
			}
		}
	}
	candidate := -1
	for rank, count := range votes {
		if count == 0 {
			continue
		}
		if candidate != -1 {
			return 0, false
		}
		candidate = rank
	}
	if candidate <= 0 || votes[candidate] < 2 {
		return 0, false
	}
	for rank, observation := range observations {
		if rank != candidate && (observation.Status != "unhealthy" || len(observation.Mask) != len(observations)) {
			return 0, false
		}
	}
	return candidate, true
}
