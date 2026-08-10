package main

import (
	"context"
	"errors"
	"log"
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

func main() {
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = ":8080"
	}

	role, err := configuredServerRole()
	if err != nil {
		log.Fatal(err)
	}
	httpHandler, closeRuntime, err := newRuntimeHandler(context.Background(), role)
	if err != nil {
		log.Fatal(err)
	}
	defer closeRuntime()

	log.Printf("tiny URL shortener %s server listening on %s", role, address)
	server := &http.Server{Addr: address, Handler: httpHandler, ReadHeaderTimeout: 5 * time.Second}
	shutdown, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverError := make(chan error, 1)
	go func() { serverError <- server.ListenAndServe() }()
	select {
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped: %v", err)
		}
	case <-shutdown.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
		cancel()
		<-serverError
	}
}

// newHandler keeps unit tests independent of external services.
func newHandler() http.Handler {
	repository := shortener.NewMemoryRepository()
	allocator := shortener.NewMemoryRangeAllocator(nil)
	ids := shortener.NewDistributedIDGenerator(allocator, 1_000)
	service := shortener.New(repository, shortener.NewIDKeyGenerator(ids))
	return rootHandler(service, configuredBaseURL(), func(context.Context) error { return nil })
}

func newRuntimeHandler(ctx context.Context, role serverRole) (http.Handler, func(), error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		repository := shortener.NewMemoryRepository()
		allocator := shortener.NewMemoryRangeAllocator(nil)
		ids := shortener.NewDistributedIDGenerator(allocator, 1_000)
		service := shortener.New(repository, shortener.NewIDKeyGenerator(ids))
		return roleHandler(role, service, configuredBaseURL(), func(context.Context) error { return nil }), func() {}, nil
	}

	postgres, err := shortener.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
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
			return nil, nil, rangeErr
		}
		allocator := shortener.NewPostgresRangeAllocator(postgres, "url_mappings")
		ids := shortener.NewDistributedIDGenerator(allocator, rangeSize)
		generator = shortener.NewIDKeyGenerator(ids)
	}
	var visits *shortener.VisitBuffer
	if role != roleCommand {
		visits = shortener.NewVisitBuffer(postgres, time.Second, 10_000)
	}
	service := shortener.NewWithVisitRecorder(repository, generator, visits)
	closeRuntime := func() {
		if visits != nil {
			flushContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if flushErr := visits.Close(flushContext); flushErr != nil {
				log.Printf("flush URL visits during shutdown: %v", flushErr)
			}
			cancel()
		}
		closeDependencies()
	}
	return roleHandler(role, service, configuredBaseURL(), postgres.Ping), closeRuntime, nil
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

func configuredBaseURL() string {
	baseURL := strings.TrimSpace(os.Getenv("SHORT_URL_BASE"))
	if baseURL == "" {
		return "http://localhost:8080"
	}
	return baseURL
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
		if request.Method == http.MethodGet && request.URL.Path == "/healthz" {
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
