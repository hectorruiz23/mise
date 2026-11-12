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
	"syscall"
	"time"

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
	tasksService := tasksapp.New(taskssqlite.New(database), clock)
	usersService := usersapp.New(userssqlite.New(database), clock)

	server, err := httpserver.New(httpserver.Options{
		DB:            database,
		Logger:        logger,
		Authenticator: usershttp.NewAuthenticator(usersService),
		PublicOrigin:  cfg.publicOrigin,
		TrustProxy:    cfg.trustProxy,
	})
	if err != nil {
		return err
	}

	tasksHandler := taskshttp.New(tasksService)
	usersHandler := usershttp.New(usersService, cfg.publicOrigin)
	server.HandleFunc("GET /api/me", false, usersHandler.Me)
	server.HandleFunc("GET /api/projects", false, tasksHandler.Projects)
	server.HandleFunc("GET /api/board", false, tasksHandler.Board)
	server.HandleFunc("GET /api/tasks", false, tasksHandler.List)
	server.HandleFunc("POST /api/tasks", false, tasksHandler.Create)
	server.HandleFunc("GET /api/tasks/{key}", false, tasksHandler.Detail)
	server.HandleFunc("PATCH /api/tasks/{key}", false, tasksHandler.Patch)
	server.HandleFunc("GET /api/tasks/{key}/movements", false, tasksHandler.Movements)
	server.HandleFunc("GET /api/tasks/{key}/leases", false, tasksHandler.ListLeases)
	server.HandleFunc("POST /api/tasks/{key}/leases", false, tasksHandler.AcquireLease)
	server.HandleFunc("PATCH /api/tasks/{key}/leases/{id}", false, tasksHandler.RenewLease)
	server.HandleFunc("DELETE /api/tasks/{key}/leases/{id}", false, tasksHandler.ReleaseLease)
	server.HandleFunc("GET /api/tasks/{key}/deliveries", false, tasksHandler.ListDeliveries)
	server.HandleFunc("POST /api/tasks/{key}/deliveries", false, tasksHandler.CreateDelivery)
	server.HandleFunc("GET /api/tasks/{key}/comments", false, tasksHandler.ListComments)
	server.HandleFunc("POST /api/tasks/{key}/comments", false, tasksHandler.CreateComment)
	server.HandleFunc("POST /api/sessions", true, usersHandler.Login)
	server.HandleFunc("DELETE /api/sessions/current", false, usersHandler.Logout)
	server.HandleFunc("POST /api/signup-requests", true, usersHandler.RequestSignup)
	server.HandleFunc("GET /api/signup-requests/{id}", true, usersHandler.SignupStatus)
	server.HandleFunc("POST /api/signup-requests/{id}/claim", true, usersHandler.ClaimSignup)
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

	return config{
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
