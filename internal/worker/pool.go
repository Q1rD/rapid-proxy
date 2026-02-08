package worker

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Q1rD/rapid-proxy/internal/selector"
)

// Errors
var (
	ErrQueueFull  = errors.New("worker pool queue is full")
	ErrPoolClosed = errors.New("worker pool is closed")
)

// Pool manages a pool of worker goroutines
type Pool struct {
	// Job queue (buffered channel)
	jobQueue chan Job

	// Configuration
	numWorkers int

	// Dependencies
	selector selector.ProxySelector

	// Lifecycle management
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Metrics (atomic for thread-safety)
	totalJobs     atomic.Int64
	successJobs   atomic.Int64
	failedJobs    atomic.Int64
	activeWorkers atomic.Int32
}

// Config contains worker pool configuration
type Config struct {
	// NumWorkers is the number of worker goroutines
	// Default: 100
	NumWorkers int

	// QueueSize is the job queue buffer size
	// Default: 1000
	QueueSize int
}

// DefaultConfig returns default worker pool configuration
func DefaultConfig() *Config {
	return &Config{
		NumWorkers: 100,
		QueueSize:  1000,
	}
}

// NewPool creates a new worker pool
func NewPool(selector selector.ProxySelector, config *Config) *Pool {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Pool{
		jobQueue:   make(chan Job, config.QueueSize),
		numWorkers: config.NumWorkers,
		selector:   selector,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start starts the worker goroutines
func (p *Pool) Start() {
	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// worker is the main worker loop
func (p *Pool) worker(id int) {
	defer p.wg.Done()

	// Track active workers
	p.activeWorkers.Add(1)
	defer p.activeWorkers.Add(-1)

	for {
		select {
		case <-p.ctx.Done():
			// Shutdown signal received
			return

		case job, ok := <-p.jobQueue:
			if !ok {
				// Queue closed
				return
			}

			// Execute the job
			p.executeJob(job)
		}
	}
}

// executeJob executes a single job
func (p *Pool) executeJob(job Job) {
	p.totalJobs.Add(1)

	start := time.Now()

	// Step 1: Select a proxy
	proxyClient, err := p.selector.Select(job.Context)
	if err != nil {
		// Failed to select proxy (all busy or context cancelled)
		p.failedJobs.Add(1)
		job.ResultCh <- Result{
			Error:    err,
			Duration: time.Since(start),
		}
		return
	}

	// Step 2: Execute request through proxy
	resp, err := proxyClient.Do(job.Request)
	duration := time.Since(start)

	if err != nil {
		// Request failed
		p.failedJobs.Add(1)
		job.ResultCh <- Result{
			Error:    err,
			ProxyURL: proxyClient.ProxyURL(),
			Duration: duration,
		}
		return
	}

	// Step 3: Read response body
	var body []byte
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()

		// Read body (ignore error, body might be large or connection issue)
		body, _ = io.ReadAll(resp.Body)
	}

	// Step 4: Send result
	p.successJobs.Add(1)
	job.ResultCh <- Result{
		Response: resp,
		Body:     body,
		ProxyURL: proxyClient.ProxyURL(),
		Duration: duration,
	}
}

// Submit submits a job to the pool (non-blocking)
// Returns ErrQueueFull if queue is full
func (p *Pool) Submit(job Job) error {
	select {
	case p.jobQueue <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// SubmitBlocking submits a job to the pool (blocking)
// Waits until queue has space or context is cancelled
func (p *Pool) SubmitBlocking(ctx context.Context, job Job) error {
	select {
	case p.jobQueue <- job:
		return nil

	case <-ctx.Done():
		return ctx.Err()

	case <-p.ctx.Done():
		return ErrPoolClosed
	}
}

// Stop gracefully stops the worker pool
// Waits for all workers to finish their current jobs
func (p *Pool) Stop() {
	// Signal shutdown
	p.cancel()

	// Close job queue (no more jobs accepted)
	close(p.jobQueue)

	// Wait for all workers to finish
	p.wg.Wait()
}

// GetStats returns current worker pool statistics
func (p *Pool) GetStats() PoolStats {
	total := p.totalJobs.Load()
	success := p.successJobs.Load()
	failed := p.failedJobs.Load()

	var successRate float64
	if total > 0 {
		successRate = float64(success) / float64(total)
	}

	return PoolStats{
		NumWorkers:    p.numWorkers,
		ActiveWorkers: int(p.activeWorkers.Load()),
		QueueSize:     len(p.jobQueue),
		QueueCapacity: cap(p.jobQueue),
		TotalJobs:     total,
		SuccessJobs:   success,
		FailedJobs:    failed,
		SuccessRate:   successRate,
	}
}

// PoolStats contains worker pool statistics
type PoolStats struct {
	NumWorkers    int     // Total number of workers
	ActiveWorkers int     // Currently active workers
	QueueSize     int     // Current queue length
	QueueCapacity int     // Queue capacity
	TotalJobs     int64   // Total jobs processed
	SuccessJobs   int64   // Successful jobs
	FailedJobs    int64   // Failed jobs
	SuccessRate   float64 // Success rate (0.0 to 1.0)
}
