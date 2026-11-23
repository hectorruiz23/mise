package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"mise/api/internal/buildinfo"
	"mise/api/internal/githubinfo"
	"mise/api/internal/panel"
	"mise/api/internal/platform/db"
	"mise/api/internal/platform/httpserver"
	taskshttp "mise/api/internal/tasks/adapters/http"
	taskssqlite "mise/api/internal/tasks/adapters/sqlite"
	tasksapp "mise/api/internal/tasks/app"
	usershttp "mise/api/internal/users/adapters/http"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersapp "mise/api/internal/users/app"
)

const shutdownTimeout = 10 * time.Second

type config struct {
	addr         string
	dbPath       string
	publicOrigin string
	trustProxy   bool
	logLevel     slog.Level
	planPolicy   tasksapp.PlanPolicy
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mise:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.logLevel}))
	slog.SetDefault(logger)

	database, err := db.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(); err != nil {
			logger.Error("close database", "error", err)
		}
	}()

	if err := httpserver.VerifyWAL(database); err != nil {
		return err
	}
	if err := db.Migrate(database); err != nil {
		return err
	}
	version, err := db.CurrentVersion(database)
	if err != nil {
		return err
	}
	logger.Info("database ready", "path", cfg.dbPath, "schema_version", version)

	clock := systemClock{}
	tasksRepo := taskssqlite.New(database)
	tasksService := tasksapp.NewWithChecks(tasksRepo, clock, githubChecks{client: githubinfo.New(githubinfo.Options{
		VerificationToken: os.Getenv("MISE_GITHUB_VERIFICATION_TOKEN"),
	})})
	usersService := usersapp.New(userssqlite.New(database), clock)

	devPrincipal := httpserver.DevPrincipalFromEnv()
	if devPrincipal != nil && !isLoopback(cfg.addr) {
		return fmt.Errorf("MISE_DEV_USER is set but %s is not a loopback address", cfg.addr)
	}

	server, err := httpserver.New(httpserver.Options{
		DB:            database,
		Logger:        logger,
		Authenticator: usershttp.NewAuthenticator(usersService),
		PublicOrigin:  cfg.publicOrigin,
		TrustProxy:    cfg.trustProxy,
		DevPrincipal:  devPrincipal,
	})
	if err != nil {
		return err
	}

	tasksHandler := taskshttp.NewWithAnchors(tasksService, githubinfo.New(githubinfo.Options{}))
	usersHandler := usershttp.New(usersService, cfg.publicOrigin)
	plans := taskshttp.NewDependencyPlans(tasksRepo, cfg.planPolicy, clock)
	graphs := taskshttp.NewGraphs(tasksRepo, cfg.planPolicy, clock)
	registerRoutes(server, tasksHandler, usersHandler, plans, graphs)
	server.SetFallback(panel.Handler())

	listener, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.addr, err)
	}
	logger.Info("listening", "addr", listener.Addr().String(), "public_origin", cfg.publicOrigin)

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if cfg.planPolicy.WorkflowID != 0 {
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan struct{}, 2)
		graph := &graphWorker{store: tasksRepo, policy: cfg.planPolicy, clock: clock, github: githubinfo.New(githubinfo.Options{
			Repository: cfg.planPolicy.Repository, VerificationToken: os.Getenv("MISE_GITHUB_VERIFICATION_TOKEN"),
		})}
		go func() { defer func() { done <- struct{}{} }(); graph.run(ctx, logger) }()
		worker := &planWorker{plans: tasksRepo, policy: cfg.planPolicy, clock: clock, github: githubinfo.New(githubinfo.Options{
			Repository: cfg.planPolicy.Repository, VerificationToken: os.Getenv("MISE_GITHUB_VERIFICATION_TOKEN"),
		})}
		go func() { defer func() { done <- struct{}{} }(); worker.run(ctx, logger) }()
		defer func() { stop(); <-done; <-done }()
	}
	integration := &integrationWorker{
		store:  tasksRepo,
		clock:  clock,
		client: &http.Client{Timeout: environmentTimeout},
		owned:  githubinfo.DefaultRepository,
	}
	if token := os.Getenv("MISE_GITHUB_MERGE_TOKEN"); token != "" {
		integration.github = githubinfo.New(githubinfo.Options{
			MergeToken: token, VerificationToken: token, Repository: githubinfo.DefaultRepository,
		})
		logger.Info("auto merge enabled", "owned_repository", integration.owned)
	} else {
		logger.Info("auto merge disabled", "reason", "MISE_GITHUB_MERGE_TOKEN is not set")
	}
	reviews := &reviewWorker{store: tasksRepo, clock: clock, github: githubinfo.New(githubinfo.Options{
		VerificationToken: os.Getenv("MISE_GITHUB_VERIFICATION_TOKEN"),
	})}
	workerCtx, workerStop := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		integration.run(workerCtx, logger)
	}()
	go func() {
		defer workers.Done()
		reviews.run(workerCtx, logger)
	}()
	defer func() { workerStop(); workers.Wait() }()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case sig := <-signals:
		logger.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		logger.Info("stopped")
		return nil
	}
}

func registerRoutes(server *httpserver.Server, tasksHandler *taskshttp.Handler, usersHandler *usershttp.Handler, plans *taskshttp.DependencyPlans, graphs *taskshttp.Graphs) {
	server.HandleReadFunc("GET /api/build", buildinfo.Handler())
	server.HandleReadFunc("GET /api/me", usersHandler.Me)
	server.HandleFunc("GET /api/public/board", true, tasksHandler.PublicBoard)
	server.HandleReadFunc("GET /api/projects", tasksHandler.Projects)
	server.HandleFunc("POST /api/projects", false, tasksHandler.CreateProject)
	server.HandleFunc("PATCH /api/projects/{key}", false, tasksHandler.PatchProject)
	server.HandleFunc("GET /api/projects/{key}/environments", false, tasksHandler.Environments)
	server.HandleFunc("POST /api/projects/{key}/environments", false, tasksHandler.AddEnvironment)
	server.HandleReadFunc("GET /api/board", tasksHandler.Board)
	server.HandleReadFunc("GET /api/tasks", tasksHandler.List)
	server.HandleFunc("POST /api/tasks", false, tasksHandler.Create)
	server.HandleReadFunc("GET /api/tasks/{key}", tasksHandler.Detail)
	server.HandleFunc("PATCH /api/tasks/{key}", false, tasksHandler.Patch)
	server.HandleReadFunc("GET /api/tasks/{key}/movements", tasksHandler.Movements)
	server.HandleReadFunc("GET /api/tasks/{key}/leases", tasksHandler.ListLeases)
	server.HandleFunc("POST /api/tasks/{key}/leases", false, tasksHandler.AcquireLease)
	server.HandleFunc("PATCH /api/tasks/{key}/leases/{id}", false, tasksHandler.RenewLease)
	server.HandleFunc("DELETE /api/tasks/{key}/leases/{id}", false, tasksHandler.ReleaseLease)
	server.HandleReadFunc("GET /api/tasks/{key}/deliveries", tasksHandler.ListDeliveries)
	server.HandleFunc("POST /api/tasks/{key}/deliveries", false, tasksHandler.CreateDelivery)
	server.HandleFunc("POST /api/tasks/{key}/submissions", false, tasksHandler.CreateSubmission)
	server.HandleReadFunc("GET /api/tasks/{key}/anchors", tasksHandler.Anchors)
	server.HandleReadFunc("GET /api/tasks/{key}/comments", tasksHandler.ListComments)
	server.HandleFunc("POST /api/tasks/{key}/comments", false, tasksHandler.CreateComment)
	server.HandleFunc("POST /api/dependency-plans", false, plans.Approve)
	server.HandleFunc("GET /api/dependency-plans/{firstTask}", false, plans.Get)
	server.HandleFunc("POST /api/dependency-plans/{firstTask}/cancel", false, plans.Cancel)
	server.HandleFunc("POST /api/plans", false, graphs.Approve)
	server.HandleFunc("GET /api/plans", false, graphs.List)
	server.HandleFunc("GET /api/plans/{id}", false, graphs.Get)
	server.HandleFunc("POST /api/plans/{id}/cancel", false, graphs.Cancel)
	server.HandleFunc("POST /api/plans/{id}/scenario", false, graphs.ReportScenario)
	server.HandleFunc("POST /api/sessions", true, usersHandler.Login)
	server.HandleFunc("DELETE /api/sessions/current", false, usersHandler.Logout)
	server.HandleFunc("POST /api/signup-requests", true, usersHandler.RequestSignup)
	server.HandleFunc("GET /api/signup-requests/{id}", true, usersHandler.SignupStatus)
	server.HandleFunc("POST /api/signup-requests/{id}/claim", true, usersHandler.ClaimSignup)
}

func loadConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("mise", flag.ContinueOnError)
	addr := flags.String("addr", envOr("MISE_ADDR", "127.0.0.1:8787"), "listen address")
	dbPath := flags.String("db", os.Getenv("MISE_DB"), "SQLite database path")
	logLevel := flags.String("log-level", envOr("MISE_LOG_LEVEL", "info"), "log level: debug, info, warn, error")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}

	if *dbPath == "" {
		return config{}, errors.New("database path is required (-db or MISE_DB)")
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return config{}, fmt.Errorf("invalid log level %q: %w", *logLevel, err)
	}

	trustProxy := false
	if raw := os.Getenv("MISE_TRUST_PROXY"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return config{}, fmt.Errorf("invalid MISE_TRUST_PROXY %q: %w", raw, err)
		}
		trustProxy = parsed
	}

	planPolicy, err := loadPlanPolicy()
	if err != nil {
		return config{}, err
	}

	return config{
		planPolicy:   planPolicy,
		addr:         *addr,
		dbPath:       *dbPath,
		publicOrigin: os.Getenv("MISE_PUBLIC_ORIGIN"),
		trustProxy:   trustProxy,
		logLevel:     level,
	}, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

type systemClock struct{}

func (systemClock) Now() int64 { return time.Now().Unix() }

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
