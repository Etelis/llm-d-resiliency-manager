// SPDX-License-Identifier: Apache-2.0
package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcore "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/Etelis/llm-d-resiliency-manager/pkg/recovery"
)

type Workload struct {
	Pods             typedcore.PodInterface
	Selector         string
	Container        string
	WorkerIndexLabel string
	ExpectedRanks    int
	RanksPerPod      int
	BasePort         int
}

func (w *Workload) Snapshot(ctx context.Context) (recovery.Snapshot, error) {
	pods, err := w.Pods.List(ctx, metav1.ListOptions{LabelSelector: w.Selector})
	if err != nil {
		return recovery.Snapshot{}, err
	}
	var members []recovery.Member
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			return recovery.Snapshot{}, fmt.Errorf("pod %s is not running or is terminating", pod.Name)
		}
		index, err := strconv.Atoi(pod.Labels[w.WorkerIndexLabel])
		if err != nil || index < 0 || index >= w.ExpectedRanks/w.RanksPerPod {
			return recovery.Snapshot{}, fmt.Errorf("pod %s has an invalid worker index", pod.Name)
		}
		var containerID string
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == w.Container && status.State.Running != nil {
				containerID = status.ContainerID
			}
		}
		if containerID == "" {
			return recovery.Snapshot{}, fmt.Errorf("pod %s has no running %s container", pod.Name, w.Container)
		}
		for local := range w.RanksPerPod {
			members = append(members, recovery.Member{Rank: index*w.RanksPerPod + local,
				PodName: pod.Name, PodUID: string(pod.UID), ContainerID: containerID,
				Endpoint: "http://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(w.BasePort+local))})
		}
	}
	if len(members) != w.ExpectedRanks {
		return recovery.Snapshot{}, fmt.Errorf("expected %d ranks, discovered %d", w.ExpectedRanks, len(members))
	}
	slices.SortFunc(members, func(a, b recovery.Member) int { return a.Rank - b.Rank })
	data, err := json.Marshal(members)
	if err != nil {
		return recovery.Snapshot{}, err
	}
	digest := sha256.Sum256(data)
	snapshot := recovery.Snapshot{Incarnation: hex.EncodeToString(digest[:]), Members: members}
	return snapshot, snapshot.Validate()
}
