// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordination "k8s.io/client-go/kubernetes/typed/coordination/v1"
	core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/Etelis/llm-d-resiliency-manager/internal/config"
	"github.com/Etelis/llm-d-resiliency-manager/internal/engine"
	"github.com/Etelis/llm-d-resiliency-manager/internal/httpjson"
	kube "github.com/Etelis/llm-d-resiliency-manager/internal/kubernetes"
	"github.com/Etelis/llm-d-resiliency-manager/internal/routing"
	"github.com/Etelis/llm-d-resiliency-manager/pkg/recovery"
)

func main() {
	if err := run(); err != nil {
		slog.Error("manager stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", "config.yaml", "manager configuration")
	kubeconfig := flag.String("kubeconfig", "", "explicit kubeconfig for local use")
	kubecontext := flag.String("context", "", "explicit Kubernetes context for local use")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	var restConfig *rest.Config
	if *kubeconfig == "" && *kubecontext == "" {
		restConfig, err = rest.InClusterConfig()
	} else if *kubeconfig == "" || *kubecontext == "" {
		return fmt.Errorf("local use requires both --kubeconfig and --context")
	} else {
		rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig}
		overrides := &clientcmd.ConfigOverrides{CurrentContext: *kubecontext}
		restConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	}
	if err != nil {
		return err
	}
	restConfig.Timeout = 10 * time.Second
	client, err := core.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	requestTimeout, _ := time.ParseDuration(cfg.RequestTimeout)
	engineHTTP, err := httpjson.New(requestTimeout, cfg.EngineTokenFile)
	if err != nil {
		return err
	}
	deadline, _ := time.ParseDuration(cfg.RecoveryTimeout)
	controller := &recovery.Controller{
		Workload: &kube.Workload{Pods: client.Pods(cfg.Namespace), Selector: cfg.PodSelector,
			Container: cfg.Container, WorkerIndexLabel: cfg.WorkerIndexLabel,
			ExpectedRanks: cfg.ExpectedRanks, RanksPerPod: cfg.RanksPerPod, BasePort: cfg.BasePort},
		Engine:  &engine.VLLM{Client: engineHTTP, Model: cfg.Model},
		Journal: &kube.MemoryJournal{},
		Options: recovery.Options{Group: cfg.Group, EnableRecovery: cfg.EnableRecovery,
			Confirmations: cfg.Confirmations, VerificationChecks: cfg.VerificationChecks, Timeout: deadline},
	}
	name := "resiliency-" + cfg.Group
	if cfg.EnableRecovery {
		routerHTTP, err := httpjson.New(requestTimeout, cfg.RoutingTokenFile)
		if err != nil {
			return err
		}
		controller.Routing = &routing.HTTP{Client: routerHTTP, Endpoint: cfg.RoutingURL}
		controller.Journal = &kube.Journal{ConfigMaps: client.ConfigMaps(cfg.Namespace), Name: name}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	server := &http.Server{Addr: ":8081", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serverErr := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			cancel()
		}
	}()
	defer server.Close()
	interval, _ := time.ParseDuration(cfg.PollInterval)
	loop := func(ctx context.Context) { reconcileLoop(ctx, controller, interval) }
	slog.Info("manager started", "group", cfg.Group, "recoveryEnabled", cfg.EnableRecovery)
	if !cfg.EnableRecovery {
		loop(ctx)
	} else {
		leases, err := coordination.NewForConfig(restConfig)
		if err != nil {
			return err
		}
		done := make(chan struct{})
		leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock: &resourcelock.LeaseLock{LeaseMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace},
				Client: leases, LockConfig: resourcelock.ResourceLockConfig{Identity: rand.Text()}},
			LeaseDuration: 30 * time.Second, RenewDeadline: 20 * time.Second, RetryPeriod: 5 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) { defer close(done); loop(ctx) },
				OnStoppedLeading: func() { cancel() },
			},
		})
		// RunOrDie can return before the reconciliation callback has stopped.
		select {
		case <-done:
		case <-time.After(requestTimeout + 15*time.Second):
		}
	}
	select {
	case err := <-serverErr:
		return err
	default:
		return nil
	}
}

func reconcileLoop(ctx context.Context, controller *recovery.Controller, interval time.Duration) {
	var previous string
	for ctx.Err() == nil {
		state, err := controller.Reconcile(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("reconcile failed", "error", err)
		}
		if state != nil {
			status := state.Incarnation + "/" + state.Phase + "/" + state.Reason
			if status != previous {
				slog.Info("group state", "phase", state.Phase, "reason", state.Reason, "incarnation", state.Incarnation)
				previous = status
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
