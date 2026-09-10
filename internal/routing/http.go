// SPDX-License-Identifier: Apache-2.0
package routing

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Etelis/llm-d-resiliency-manager/internal/httpjson"
	"github.com/Etelis/llm-d-resiliency-manager/pkg/recovery"
)

type HTTP struct {
	Client   *httpjson.Client
	Endpoint string
}

func (r *HTTP) Apply(ctx context.Context, update recovery.RoutingUpdate) (bool, error) {
	var response struct {
		Group       string `json:"group"`
		Incarnation string `json:"incarnation"`
		OperationID string `json:"operationID"`
		Revision    int    `json:"revision"`
		Applied     bool   `json:"applied"`
	}
	if err := r.Client.Do(ctx, http.MethodPut, r.Endpoint, update, &response, http.StatusOK); err != nil {
		return false, err
	}
	if response.Group != update.Group || response.Incarnation != update.Incarnation ||
		response.OperationID != update.OperationID || response.Revision != update.Revision {
		return false, fmt.Errorf("routing acknowledgement does not match the requested operation")
	}
	return response.Applied, nil
}
