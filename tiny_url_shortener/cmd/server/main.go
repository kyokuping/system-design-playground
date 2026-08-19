package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/handler"
	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/shortener"
)

// main keeps the exit status honest: run releases its dependencies through its
// own defers before main turns a startup or runtime failure into a non-zero exit.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = ":8080"
	}
	metricsAddress := os.Getenv("METRICS_ADDR")
	if metricsAddress == "" {
		metricsAddress = ":9090"
	}

	role, err := configuredServerRole()
	if err != nil {
		return err
	}
	httpHandler, metricsHandler, closeRuntime, err := newRuntimeHandler(context.Background(), role)
	if err != nil {
		return err
	}
	defer closeRuntime()

	shutdown, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(shutdown, &http.Server{
		Addr:              address,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}, role, &http.Server{
		Addr:              metricsAddress,
		Handler:           metricsHandler,
		ReadHeaderTimeout: 5 * time.Second,
	})
}

// serve binds before announcing the address so the startup log reports a port
// the server actually holds, and reports a failed bind to the caller instead of
// leaving it as a log line behind a successful exit.
func serve(shutdown context.Context, server *http.Server, role serverRole, additional ...*http.Server) error {
	servers := append([]*http.Server{server}, additional...)
	listeners := make([]net.Listener, 0, len(servers))
	for _, current := range servers {
		listener, err := net.Listen("tcp", current.Addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return err
		}
		listeners = append(listeners, listener)
	}

	log.Printf("tiny URL shortener %s server listening on %s", role, listeners[0].Addr())
	for _, listener := range listeners[1:] {
		log.Printf("tiny URL shortener metrics server listening on %s", listener.Addr())
	}
	serverError := make(chan error, len(servers))
	for index, current := range servers {
		go func() { serverError <- current.Serve(listeners[index]) }()
	}
	shutdownServers := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, current := range servers {
			if err := current.Shutdown(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	select {
	case err := <-serverError:
		shutdownErr := shutdownServers()
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return shutdownErr
	case <-shutdown.Done():
		return shutdownServers()
	}
}

// newHandler keeps unit tests independent of external services.
func newHandler() http.Handler {
	repository := shortener.NewMemoryRepository()
	allocator := shortener.NewMemoryRangeAllocator(nil)
	ids := shortener.NewDistributedIDGenerator(allocator, 1_000)
	service := shortener.New(repository, shortener.NewIDKeyGenerator(ids))
	return rootHandler(service, "http://localhost:8080", func(context.Context) error { return nil })
}

func newRuntimeHandler(ctx context.Context, role serverRole) (http.Handler, http.Handler, func(), error) {
	baseURL, err := configuredBaseURL(role)
	if err != nil {
		return nil, nil, nil, err
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		repository := shortener.NewMemoryRepository()
		allocator := shortener.NewMemoryRangeAllocator(nil)
		ids := shortener.NewDistributedIDGenerator(allocator, 1_000)
		service := shortener.New(repository, shortener.NewIDKeyGenerator(ids))
		application, metrics := newMetricsHandlers(roleHandler(role, service, baseURL, func(context.Context) error { return nil }), nil)
		return application, metrics, func() {}, nil
	}

	postgres, err := shortener.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	var repository shortener.URLRepository = postgres
	closeDependencies := func() { postgres.Close() }

	if redisAddress := strings.TrimSpace(os.Getenv("REDIS_ADDR")); redisAddress != "" {
		cache, cacheErr := shortener.OpenRedis(ctx, redisAddress)
		if cacheErr != nil {
			log.Printf("Redis unavailable; using PostgreSQL directly: %v", cacheErr)
		} else {
			repository = shortener.NewCachedRepository(postgres, cache, time.Hour, 30*time.Second)
			closeDependencies = func() { _ = cache.Close(); postgres.Close() }
		}
	}

	var generator shortener.KeyGenerator
	if role != roleRedirect {
		rangeSize, rangeErr := configuredRangeSize()
		if rangeErr != nil {
			closeDependencies()
			return nil, nil, nil, rangeErr
		}
		allocator := shortener.NewPostgresRangeAllocator(postgres, "url_mappings")
		ids := shortener.NewDistributedIDGenerator(allocator, rangeSize)
		generator = shortener.NewIDKeyGenerator(ids)
	}
	// Keep the recorder interface nil for roles without a buffer. Assigning a nil
	// *VisitBuffer to it would leave the interface non-nil and panic on use.
	var buffer *shortener.VisitBuffer
	var visits shortener.URLVisitRecorder
	if role != roleCommand {
		buffer = shortener.NewVisitBuffer(postgres, time.Second, 10_000)
		visits = buffer
	}
	service := shortener.NewWithVisitRecorder(repository, generator, visits)
	closeRuntime := func() {
		if buffer != nil {
			flushContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if flushErr := buffer.Close(flushContext); flushErr != nil {
				log.Printf("flush URL visits during shutdown: %v", flushErr)
			}
			cancel()
		}
		closeDependencies()
	}
	application, metrics := newMetricsHandlers(roleHandler(role, service, baseURL, postgres.Ping), buffer)
	return application, metrics, closeRuntime, nil
}

type serverRole string

const (
	roleAll      serverRole = "all"
	roleCommand  serverRole = "command"
	roleRedirect serverRole = "redirect"
)

func configuredServerRole() (serverRole, error) {
	role := serverRole(strings.ToLower(strings.TrimSpace(os.Getenv("SERVER_ROLE"))))
	if role == "" {
		return roleAll, nil
	}
	switch role {
	case roleAll, roleCommand, roleRedirect:
		return role, nil
	default:
		return "", errors.New("SERVER_ROLE must be one of: all, command, redirect")
	}
}

func configuredRangeSize() (uint64, error) {
	value := strings.TrimSpace(os.Getenv("ID_RANGE_SIZE"))
	if value == "" {
		return 1_000, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, errors.New("ID_RANGE_SIZE must be a positive integer")
	}
	return parsed, nil
}

// configuredBaseURL resolves the origin that short URLs are minted under. Roles
// that mint URLs must be told explicitly: defaulting to localhost would hand
// every caller an unreachable link instead of failing at startup.
func configuredBaseURL(role serverRole) (string, error) {
	baseURL := strings.TrimSpace(os.Getenv("SHORT_URL_BASE"))
	if baseURL == "" && role != roleRedirect {
		return "", errors.New("SHORT_URL_BASE is required for the " + string(role) + " role")
	}
	return baseURL, nil
}

func rootHandler(
	service handler.URLService,
	baseURL string,
	readinessProbe func(context.Context) error,
) http.Handler {
	return roleHandler(roleAll, service, baseURL, readinessProbe)
}

func roleHandler(
	role serverRole,
	service handler.URLService,
	baseURL string,
	readinessProbe func(context.Context) error,
) http.Handler {
	var urlHandler http.Handler
	switch role {
	case roleCommand:
		urlHandler = handler.NewCommandHandler(service, baseURL)
	case roleRedirect:
		urlHandler = handler.NewRedirectHandler(service)
	default:
		urlHandler = handler.NewURLHandler(service, baseURL)
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/-/healthz" {
			if err := readinessProbe(request.Context()); err != nil {
				http.Error(response, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			response.WriteHeader(http.StatusNoContent)
			return
		}
		urlHandler.ServeHTTP(response, request)
	})
}
