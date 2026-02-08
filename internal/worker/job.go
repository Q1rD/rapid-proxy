package worker

import (
	"context"
	"net/http"
	"time"
)

// Job represents a single HTTP request job to be executed
type Job struct {
	// The HTTP request to execute
	Request *http.Request

	// Context for cancellation and timeouts
	Context context.Context

	// Result channel (buffered with size 1 to avoid blocking worker)
	ResultCh chan Result
}

// Result contains the result of job execution
type Result struct {
	// HTTP response (nil if error occurred)
	Response *http.Response

	// Response body (read for convenience)
	Body []byte

	// Proxy URL that was used
	ProxyURL string

	// Total duration of the request
	Duration time.Duration

	// Error if request failed
	Error error
}

// NewJob creates a new job from HTTP request
func NewJob(req *http.Request) Job {
	return Job{
		Request:  req,
		Context:  req.Context(),
		ResultCh: make(chan Result, 1), // Buffered to avoid blocking
	}
}
