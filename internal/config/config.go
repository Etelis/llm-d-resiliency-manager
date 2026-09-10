// SPDX-License-Identifier: Apache-2.0
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

type Config struct {
	Group              string `json:"group"`
	Namespace          string `json:"namespace"`
	PodSelector        string `json:"podSelector"`
	Container          string `json:"container"`
	WorkerIndexLabel   string `json:"workerIndexLabel"`
	ExpectedRanks      int    `json:"expectedRanks"`
	RanksPerPod        int    `json:"ranksPerPod"`
	BasePort           int    `json:"basePort"`
	EnableRecovery     bool   `json:"enableRecovery"`
	PollInterval       string `json:"pollInterval"`
	RequestTimeout     string `json:"requestTimeout"`
	RecoveryTimeout    string `json:"recoveryTimeout"`
	Confirmations      int    `json:"confirmations"`
	VerificationChecks int    `json:"verificationChecks"`
	Model              string `json:"model"`
	EngineTokenFile    string `json:"engineTokenFile"`
	RoutingURL         string `json:"routingURL"`
	RoutingTokenFile   string `json:"routingTokenFile"`
}

func Load(path string) (Config, error) {
	c := Config{Namespace: os.Getenv("POD_NAMESPACE"), Container: "vllm",
		WorkerIndexLabel: "leaderworkerset.sigs.k8s.io/worker-index",
		RanksPerPod:      1, BasePort: 8000, PollInterval: "2s", RequestTimeout: "10s",
		RecoveryTimeout: "120s", Confirmations: 2, VerificationChecks: 3}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := yaml.UnmarshalStrict(data, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if len(c.Group) > 50 || len(validation.IsDNS1123Label(c.Group)) != 0 ||
		len(validation.IsDNS1123Label(c.Namespace)) != 0 {
		return fmt.Errorf("group and namespace must be DNS labels; group must be at most 50 characters")
	}
	selector, err := labels.Parse(c.PodSelector)
	if err != nil || selector.Empty() {
		return fmt.Errorf("podSelector must select exactly one EP group")
	}
	if c.Container == "" || len(validation.IsQualifiedName(c.WorkerIndexLabel)) != 0 {
		return fmt.Errorf("container and workerIndexLabel must be set")
	}
	if c.ExpectedRanks < 3 || c.ExpectedRanks > 256 || c.RanksPerPod < 1 ||
		c.ExpectedRanks%c.RanksPerPod != 0 || c.BasePort < 1 || c.BasePort+c.RanksPerPod-1 > 65535 {
		return fmt.Errorf("invalid rank layout or port range (supported group size: 3–256)")
	}
	for name, value := range map[string]string{"pollInterval": c.PollInterval,
		"requestTimeout": c.RequestTimeout, "recoveryTimeout": c.RecoveryTimeout} {
		d, err := time.ParseDuration(value)
		if err != nil || d < time.Second || d > time.Hour {
			return fmt.Errorf("%s must be between 1s and 1h", name)
		}
	}
	if c.Confirmations < 2 || c.VerificationChecks < 2 {
		return fmt.Errorf("confirmations and verificationChecks must each be at least 2")
	}
	if c.EnableRecovery && (strings.TrimSpace(c.Model) == "" || c.RoutingURL == "") {
		return fmt.Errorf("active recovery requires model and routingURL")
	}
	if c.RoutingURL != "" {
		u, err := url.Parse(c.RoutingURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("routingURL must be an HTTP(S) endpoint without embedded credentials, query or fragment")
		}
	}
	return nil
}
