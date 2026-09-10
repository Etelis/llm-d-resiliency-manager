// SPDX-License-Identifier: Apache-2.0
package engine

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Etelis/llm-d-resiliency-manager/internal/httpjson"
	"github.com/Etelis/llm-d-resiliency-manager/pkg/recovery"
)

type VLLM struct {
	Client *httpjson.Client
	Model  string
}

func (v *VLLM) Observe(ctx context.Context, member recovery.Member) (recovery.Observation, error) {
	var response struct {
		Version int `json:"schema_version"`
		Total   int `json:"total_engines"`
		Engines []struct {
			ID *int `json:"id"`
			recovery.Observation
		} `json:"engines"`
	}
	err := v.Client.Do(ctx, http.MethodGet, member.Endpoint+"/v1/fault_tolerance/status", nil, &response, http.StatusOK)
	if err != nil {
		return recovery.Observation{}, err
	}
	if response.Version != 1 || response.Total != 1 || len(response.Engines) != 1 ||
		response.Engines[0].ID == nil || *response.Engines[0].ID != member.Rank {
		return recovery.Observation{}, fmt.Errorf("rank %d: unsupported status schema or engine identity", member.Rank)
	}
	observation := response.Engines[0].Observation
	if observation.Status != "healthy" && observation.Status != "unhealthy" && observation.Status != "dead" {
		return recovery.Observation{}, fmt.Errorf("rank %d: unknown engine status", member.Rank)
	}
	return observation, nil
}

func (v *VLLM) ScaleDown(ctx context.Context, member recovery.Member, op recovery.Operation) error {
	body := struct {
		Instruction string           `json:"instruction"`
		Params      map[string][]int `json:"params"`
		RequestID   string           `json:"request_id"`
	}{"scale_down", map[string][]int{"removed_dp_ranks": {op.Removed}}, op.ID}
	var response struct {
		RequestID string `json:"request_id"`
	}
	if err := v.Client.Do(ctx, http.MethodPost, member.Endpoint+"/v1/fault_tolerance/apply", body, &response, http.StatusAccepted); err != nil {
		return fmt.Errorf("rank %d: %w", member.Rank, err)
	}
	if response.RequestID != op.ID {
		return fmt.Errorf("rank %d: recovery response has a different request ID", member.Rank)
	}
	return nil
}

func (v *VLLM) Verify(ctx context.Context, member recovery.Member) error {
	body := map[string]any{"model": v.Model, "prompt": "Reply with one word: ready.",
		"max_tokens": 8, "temperature": 0, "seed": 42, "stream": false}
	var response struct {
		Choices []struct {
			Text         string `json:"text"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := v.Client.Do(ctx, http.MethodPost, member.Endpoint+"/v1/completions", body, &response, http.StatusOK); err != nil {
		return fmt.Errorf("rank %d: %w", member.Rank, err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Text == "" ||
		(response.Choices[0].FinishReason != "stop" && response.Choices[0].FinishReason != "length") {
		return fmt.Errorf("rank %d: incomplete inference response", member.Rank)
	}
	return nil
}
