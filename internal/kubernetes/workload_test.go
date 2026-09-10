// SPDX-License-Identifier: Apache-2.0
package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcore "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

func TestDiscoveryTracksProcessesAndRejectsIncompleteGroups(t *testing.T) {
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "model-0", UID: "uid-0", Labels: map[string]string{"index": "0"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.1", ContainerStatuses: []corev1.ContainerStatus{
				{Name: "vllm", ContainerID: "container-0", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "model-1", UID: "uid-1", Labels: map[string]string{"index": "1"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.2", ContainerStatuses: []corev1.ContainerStatus{
				{Name: "vllm", ContainerID: "container-1", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "group=model" {
			t.Error("missing group selector")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: pods})
	}))
	defer server.Close()
	client, err := typedcore.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	w := Workload{Pods: client.Pods("test"), Selector: "group=model", Container: "vllm", WorkerIndexLabel: "index", ExpectedRanks: 4, RanksPerPod: 2, BasePort: 8000}
	original, err := w.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if original.Members[3].Endpoint != "http://10.0.0.2:8001" {
		t.Fatal("incorrect rank-to-endpoint mapping")
	}
	slices.Reverse(pods)
	reordered, err := w.Snapshot(context.Background())
	if err != nil || reordered.Incarnation != original.Incarnation {
		t.Fatal("list ordering changed membership")
	}
	pods[0].Status.ContainerStatuses[0].ContainerID = "replacement"
	restarted, err := w.Snapshot(context.Background())
	if err != nil || restarted.Incarnation == original.Incarnation {
		t.Fatal("container restart did not change membership")
	}
	pods[0].Labels["index"] = "0"
	if _, err := w.Snapshot(context.Background()); err == nil {
		t.Fatal("duplicate ranks accepted")
	}
	pods = pods[:1]
	if _, err := w.Snapshot(context.Background()); err == nil {
		t.Fatal("incomplete group accepted")
	}
}
