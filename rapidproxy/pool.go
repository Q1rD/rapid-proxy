package rapidproxy

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Q1rD/rapid-proxy/internal/connection"
	"github.com/Q1rD/rapid-proxy/internal/health"
	"github.com/Q1rD/rapid-proxy/internal/metrics"
	"github.com/Q1rD/rapid-proxy/internal/ratelimit"
	"github.com/Q1rD/rapid-proxy/internal/selector"
	"github.com/Q1rD/rapid-proxy/internal/worker"
)

// Pool is the main entry point for rapid-proxy
type Pool struct {
	config *Config

	// Core components
	manager       *connection.Manager
	selector      *selector.RoundRobinSelector
	workerPool    *worker.Pool
	collector     *metrics.Collector
	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new proxy pool
func New(config *Config) (*Pool, error) {
	// 1. Validate and apply defaults
	if config == nil {
		config = DefaultConfig()
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	config.ApplyDefaults()

	// 2. Create connection manager
	managerConfig := &connection.Config{
		ProxyURLs: config.ProxyURLs,
		TransportConfig: &connection.TransportConfig{
			MaxIdleConns:          config.MaxIdleConns,
			MaxIdleConnsPerHost:   config.MaxIdleConnsPerHost,
			MaxConnsPerHost:       config.MaxConcurrentPerProxy,
			IdleConnTimeout:       config.IdleConnTimeout,
			DialTimeout:           config.DialTimeout,
			TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
			KeepAlive:             config.KeepAlive,
			ResponseHeaderTimeout: config.ResponseHeaderTimeout,
			InsecureSkipVerify:    config.InsecureSkipVerify,
		},
		RequestTimeout:      config.RequestTimeout,
		IdleCleanupInterval: config.IdleCleanupInterval,
		IdleCleanupTimeout:  config.IdleCleanupTimeout,
	}

	manager, err := connection.NewManager(managerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection manager: %w", err)
	}

	// 3. Get all clients
	clients := manager.GetClients()

	// 4. Set rate limiters for each proxy
	for _, client := range clients {
		limiter := ratelimit.NewTokenBucket(
			config.RateLimitPerProxy,
			config.RateLimitPerProxy,
		)
		client.SetRateLimiter(limiter)
	}

	// 5. Set circuit breakers for each proxy
	for _, client := range clients {
		cbConfig := &health.Config{
			FailureThreshold:  config.FailureThreshold,
			DegradedThreshold: config.DegradedThreshold,
			MinRequests:       config.MinRequestsForTrip,
			RecoveryTimeout:   config.RecoveryTimeout,
			WindowSize:        60 * time.Second,
		}
		cb := health.NewCircuitBreaker(cbConfig)
		client.SetCircuitBreaker(cb)
	}

	// 5.5. Set per-proxy concurrency semaphore (optional)
	if config.MaxConcurrentPerProxy > 0 {
		for _, client := range clients {
			client.SetConcurrencySemaphore(config.MaxConcurrentPerProxy)
		}
	}

	// 5.6. Set per-proxy domain rate limiters (optional)
	if len(config.DomainRateLimits) > 0 {
		for _, client := range clients {
			client.SetDomainLimiter(ratelimit.NewDomainLimiter(config.DomainRateLimits))
		}
	}

	// 6. Create selector
	selectorConfig := &selector.Config{
		MaxRetries: len(clients),
	}
	sel, err := selector.NewRoundRobinSelector(clients, selectorConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create selector: %w", err)
	}

	// 7. Create worker pool
	workerConfig := &worker.Config{
		NumWorkers: config.NumWorkers,
		QueueSize:  config.QueueSize,
	}
	wp := worker.NewPool(sel, workerConfig)

	// 8. Create metrics collector
	collector := metrics.NewCollector(manager)

	// 9. Create pool
	ctx, cancel := context.WithCancel(context.Background())

	pool := &Pool{
		config:        config,
		manager:       manager,
		selector:      sel,
		workerPool:    wp,
		collector: collector,
		ctx:       ctx,
		cancel:        cancel,
	}

	// 10. Start worker pool
	wp.Start()

	return pool, nil
}

// Do executes a single HTTP request
func (p *Pool) Do(req *http.Request) (*Result, error) {
	return p.DoWithContext(context.Background(), req)
}

// DoWithContext executes a request with context
func (p *Pool) DoWithContext(ctx context.Context, req *http.Request) (*Result, error) {
	// Create job
	job := worker.NewJob(req.Clone(ctx))

	// Submit to worker pool (blocking)
	if err := p.workerPool.SubmitBlocking(ctx, job); err != nil {
		return nil, err
	}

	// Wait for result
	select {
	case result := <-job.ResultCh:
		return &Result{
			Response:  result.Response,
			Body:      result.Body,
			ProxyUsed: result.ProxyURL,
			Latency:   result.Duration,
			Error:     result.Error,
		}, nil

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-p.ctx.Done():
		return nil, ErrPoolClosed
	}
}

// DoBatch executes a batch of requests
func (p *Pool) DoBatch(requests []*http.Request) ([]*Result, error) {
	return p.DoBatchWithContext(context.Background(), requests)
}

// DoBatchWithContext executes a batch of requests with context
func (p *Pool) DoBatchWithContext(ctx context.Context, requests []*http.Request) ([]*Result, error) {
	results := make([]*Result, len(requests))

	// Create and submit jobs
	jobs := make([]worker.Job, len(requests))
	for i, req := range requests {
		jobs[i] = worker.NewJob(req.Clone(ctx))
		if err := p.workerPool.Submit(jobs[i]); err != nil {
			results[i] = &Result{Error: err}
		}
	}

	// Collect results
	var wg sync.WaitGroup
	for i := range jobs {
		if results[i] != nil {
			continue // Already has error
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			select {
			case workerResult := <-jobs[idx].ResultCh:
				results[idx] = &Result{
					Response:  workerResult.Response,
					Body:      workerResult.Body,
					ProxyUsed: workerResult.ProxyURL,
					Latency:   workerResult.Duration,
					Error:     workerResult.Error,
				}

			case <-ctx.Done():
				results[idx] = &Result{Error: ctx.Err()}

			case <-p.ctx.Done():
				results[idx] = &Result{Error: ErrPoolClosed}
			}
		}(i)
	}

	wg.Wait()

	return results, nil
}

// Stats returns global pool statistics
func (p *Pool) Stats() *Stats {
	stats := p.collector.GetStats()
	workerStats := p.workerPool.GetStats()

	stats.WorkerCount = workerStats.NumWorkers
	stats.QueueSize = workerStats.QueueSize
	stats.QueueCapacity = workerStats.QueueCapacity

	return stats
}

// ProxyStats returns per-proxy statistics
func (p *Pool) ProxyStats() []*ProxyStats {
	return p.collector.GetProxyStats()
}

// Close gracefully shuts down the pool
func (p *Pool) Close() error {
	// 1. Cancel context
	p.cancel()

	// 2. Stop worker pool (graceful)
	p.workerPool.Stop()

	// 3. Close manager
	return p.manager.Close()
}

// Type aliases from metrics package
type Stats = metrics.Stats
type ProxyStats = metrics.ProxyStats
