// Command server is the entrypoint for the fraud-signals scoring service.
//
// It serves the SAME scoring logic over two transports — REST (public edge) and
// gRPC (internal callers) — backed by one application service, and shuts both
// down gracefully on SIGTERM so rolling ECS deploys don't drop in-flight requests.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/blocklocmedia/fraud-signals/internal/auth"
	"github.com/blocklocmedia/fraud-signals/internal/awscfg"
	"github.com/blocklocmedia/fraud-signals/internal/config"
	"github.com/blocklocmedia/fraud-signals/internal/events"
	"github.com/blocklocmedia/fraud-signals/internal/grpcapi"
	"github.com/blocklocmedia/fraud-signals/internal/httpapi"
	"github.com/blocklocmedia/fraud-signals/internal/metrics"
	"github.com/blocklocmedia/fraud-signals/internal/providers"
	"github.com/blocklocmedia/fraud-signals/internal/scorer"
	"github.com/blocklocmedia/fraud-signals/internal/service"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := config.Load()

	// --- core wiring: providers -> guard -> scorer -> service ----------------
	// Per-vendor metrics: EMF to stdout in AWS (feeds the per-vendor alarm), or
	// the in-memory collector locally. Same Recorder port either way.
	var rec metrics.Recorder = metrics.NewCollector()
	if cfg.MetricsEMF {
		rec = metrics.NewEMFRecorder(os.Stdout, cfg.MetricsNamespace)
	}
	prov := providers.GuardSet(providers.DefaultProviders(), cfg.Guard, rec)
	sc := scorer.New(prov.Independent, prov.Dependent, cfg.Scorer, log)

	// Event publisher: the durable handoff off the request path. With async
	// enabled we publish to SQS; otherwise events go to the structured log
	// (local/dev, no AWS needed). The consumer does the heavy DynamoDB write.
	publisher := buildPublisher(cfg, log)
	svc := service.New(sc, publisher, cfg.PersistMode, log)

	// --- pluggable auth validator --------------------------------------------
	authValidator := pickValidator(cfg, log)

	// --- HTTP edge -----------------------------------------------------------
	probe := httpapi.NewProbe()
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Scorer: svc,
		Demo:   buildDemo(cfg, log),
		Auth:   authValidator,
		Probe:  probe,
		Log:    log,
	})
	httpSrv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// --- gRPC edge -----------------------------------------------------------
	grpcSrv := grpc.NewServer(grpcapi.ChainUnary(authValidator, log))
	grpcapi.Register(grpcSrv, grpcapi.NewServer(svc))

	// --- run both, then block on a shutdown signal ---------------------------
	// signal.NotifyContext cancels rootCtx on SIGINT/SIGTERM (ECS sends SIGTERM
	// before SIGKILL on task stop / rolling deploy).
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 2)

	go func() {
		log.Info("http listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	go func() {
		lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
		if err != nil {
			serveErr <- err
			return
		}
		log.Info("grpc listening", "addr", lis.Addr().String())
		if err := grpcSrv.Serve(lis); err != nil {
			serveErr <- err
		}
	}()

	// We're up: mark ready so the ALB starts routing.
	probe.SetReady(true)
	log.Info("service ready",
		"persist_mode", string(cfg.PersistMode),
		"auth_enabled", cfg.AuthEnabled,
		"demo_endpoints", cfg.DemoEndpoints)

	select {
	case err := <-serveErr:
		log.Error("server error, shutting down", "error", err.Error())
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	}

	gracefulShutdown(httpSrv, grpcSrv, probe, cfg, log)
}

// gracefulShutdown drains both transports without dropping in-flight requests.
//
// Order matters: first flip readiness to NOT-ready and pause, so the ALB health
// check fails and the target deregisters BEFORE we stop accepting connections —
// otherwise requests routed during the gap would be refused. Then drain HTTP
// (Shutdown waits for active requests) and gRPC (GracefulStop) concurrently,
// bounded by ShutdownTimeout.
func gracefulShutdown(httpSrv *http.Server, grpcSrv *grpc.Server, probe *httpapi.Probe, cfg config.Config, log *slog.Logger) {
	probe.SetReady(false)
	log.Info("readiness set to draining; waiting for load balancer to deregister",
		"drain_delay", cfg.ShutdownDrainDelay.String())
	time.Sleep(cfg.ShutdownDrainDelay)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Error("http graceful shutdown failed; forcing close", "error", err.Error())
			_ = httpSrv.Close()
		}
	}()

	go func() {
		defer wg.Done()
		// GracefulStop blocks until in-flight RPCs finish; race it against the
		// deadline so a stuck RPC can't hang the deploy forever.
		done := make(chan struct{})
		go func() { grpcSrv.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			log.Error("grpc graceful shutdown timed out; forcing stop")
			grpcSrv.Stop()
		}
	}()

	wg.Wait()
	log.Info("shutdown complete")
}

// pickValidator returns the configured auth validator, or nil to disable auth.
// Production uses HS256 with a secret injected from Secrets Manager/SSM; nil
// (open edge) is only for local/dev.
func pickValidator(cfg config.Config, log *slog.Logger) auth.Validator {
	if !cfg.AuthEnabled {
		log.Warn("AUTH DISABLED — edge is open; enable AUTH_ENABLED in non-local envs")
		return nil
	}
	if cfg.JWTSecret == "" {
		log.Error("AUTH_ENABLED but JWT_SECRET is empty; refusing to start with broken auth")
		os.Exit(1)
	}
	return auth.NewHS256([]byte(cfg.JWTSecret))
}

// buildPublisher returns an SQS publisher when async is enabled and a queue URL
// is set, otherwise a log publisher (local/dev). A misconfigured async setup
// (enabled but no queue) is fatal — silently dropping the audit trail is worse.
func buildPublisher(cfg config.Config, log *slog.Logger) events.Publisher {
	if !cfg.AsyncEnabled {
		log.Warn("async disabled — scoring events go to the log, not SQS")
		return events.NewLogPublisher(log)
	}
	if cfg.QueueURL == "" {
		log.Error("ASYNC_ENABLED but AUDIT_QUEUE_URL is empty; refusing to start")
		os.Exit(1)
	}
	awsCfg, err := awscfg.Load(context.Background())
	if err != nil {
		log.Error("failed to load AWS config", "error", err.Error())
		os.Exit(1)
	}
	log.Info("async enabled — publishing scoring events to SQS", "queue_url", cfg.QueueURL)
	return events.NewSQSPublisher(awscfg.SQS(awsCfg), cfg.QueueURL)
}

// buildDemo wires the incident demo endpoints (sick vendor set scored two ways)
// when enabled.
func buildDemo(cfg config.Config, log *slog.Logger) *httpapi.Demo {
	if !cfg.DemoEndpoints {
		return nil
	}
	sick := providers.DemoSickProviders()
	naive := scorer.NewNaive(sick.Independent, sick.Dependent, cfg.Scorer, log)
	guardedSick := providers.GuardSet(providers.DemoSickProviders(), cfg.Guard, metrics.NewCollector())
	guarded := scorer.New(guardedSick.Independent, guardedSick.Dependent, cfg.Scorer, log)
	log.Info("incident demo endpoints enabled",
		"naive", "POST /demo/naive/score", "guarded", "POST /demo/guarded/score")
	return &httpapi.Demo{Naive: naive, Guarded: guarded}
}
