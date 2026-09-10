// SPDX-License-Identifier: Apache-2.0
package kubernetes

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcore "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/Etelis/llm-d-resiliency-manager/pkg/recovery"
)

type Journal struct {
	ConfigMaps typedcore.ConfigMapInterface
	Name       string
}

func (j *Journal) Load(ctx context.Context) (*recovery.State, error) {
	cm, err := j.ConfigMaps.Get(ctx, j.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state recovery.State
	if err := json.Unmarshal([]byte(cm.Data["state.json"]), &state); err != nil {
		return nil, err
	}
	state.Version = cm.ResourceVersion
	return &state, state.Validate()
}

func (j *Journal) Save(ctx context.Context, state *recovery.State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: j.Name, ResourceVersion: state.Version},
		Data: map[string]string{"state.json": string(data)}}
	if state.Version == "" {
		cm, err = j.ConfigMaps.Create(ctx, cm, metav1.CreateOptions{})
	} else {
		cm, err = j.ConfigMaps.Update(ctx, cm, metav1.UpdateOptions{})
	}
	if err == nil {
		state.Version = cm.ResourceVersion
	}
	return err
}

// MemoryJournal is used for observation; it never writes cluster state.
type MemoryJournal struct{ data []byte }

func (j *MemoryJournal) Load(context.Context) (*recovery.State, error) {
	if j.data == nil {
		return nil, nil
	}
	var state recovery.State
	err := json.Unmarshal(j.data, &state)
	return &state, err
}

func (j *MemoryJournal) Save(_ context.Context, state *recovery.State) error {
	data, err := json.Marshal(state)
	if err == nil {
		j.data = data
	}
	return err
}
