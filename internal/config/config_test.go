// SPDX-License-Identifier: Apache-2.0
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryMustBeExplicitAndConfigured(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "test")
	base := "group: test\npodSelector: group=test\nexpectedRanks: 4\n"
	for _, tt := range []struct {
		name, extra   string
		valid, active bool
	}{
		{"observe by default", "", true, false},
		{"missing routing adapter", "enableRecovery: true\nmodel: test\n", false, false},
		{"active", "enableRecovery: true\nmodel: test\nroutingURL: https://router.test/membership\n", true, true},
		{"misspelled option", "enableRecover: true\n", false, false},
		{"unbounded requests", "requestTimeout: 0s\n", false, false},
		{"insufficient evidence", "confirmations: 1\n", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(base+tt.extra), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err == nil) != tt.valid {
				t.Fatalf("Load error = %v", err)
			}
			if err == nil && cfg.EnableRecovery != tt.active {
				t.Fatal("unexpected recovery mode")
			}
		})
	}
}
