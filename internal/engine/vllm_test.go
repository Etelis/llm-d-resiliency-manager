// SPDX-License-Identifier: Apache-2.0
package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Etelis/llm-d-resiliency-manager/internal/httpjson"
	"github.com/Etelis/llm-d-resiliency-manager/pkg/recovery"
)

func TestStatusRejectsWrongEngineIdentity(t *testing.T) {
	for _, payload := range []string{
		`{"schema_version":1,"total_engines":1,"engines":[{"id":2,"status":"healthy"}]}`,
		`{"schema_version":1,"total_engines":1,"engines":[{"status":"healthy"}]}`,
		`{"schema_version":2,"total_engines":1,"engines":[{"id":0,"status":"healthy"}]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, payload) }))
		client, err := httpjson.New(time.Second, "")
		if err != nil {
			t.Fatal(err)
		}
		v := VLLM{Client: client}
		_, err = v.Observe(context.Background(), recovery.Member{Rank: 0, Endpoint: server.URL})
		server.Close()
		if err == nil {
			t.Fatalf("accepted %s", payload)
		}
	}
}
