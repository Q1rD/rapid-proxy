package rapidproxy

import (
	"net/http"
	"time"
)

// Result represents the outcome of an HTTP request executed through the proxy pool.
// It contains both successful responses and error information, allowing callers
// to handle all cases uniformly.
//
// Important: Check both Error and Response.StatusCode to fully understand the outcome:
//   - If Error != nil: Request failed at the network/pool level (no response received)
//   - If Error == nil but Response.StatusCode >= 400: Request succeeded but HTTP error returned
//   - If Error == nil and Response.StatusCode < 400: Complete success
//
// Example usage:
//
//	result, err := pool.Do(req)
//	if err != nil {
//	    // Pool-level error (queue full, all proxies busy, etc.)
//	    return err
//	}
//	if result.Error != nil {
//	    // Request-level error (timeout, connection refused, circuit breaker tripped, etc.)
//	    return result.Error
//	}
//	if result.Response.StatusCode >= 400 {
//	    // HTTP error (request went through but server returned error)
//	    return fmt.Errorf("HTTP %d", result.Response.StatusCode)
//	}
//	// Success - use result.Body
type Result struct {
	// Response is the HTTP response received from the target server.
	// Nil if Error is non-nil.
	Response *http.Response

	// Body is the response body, read for convenience.
	// Empty if Error is non-nil or if response had no body.
	// The body is fully read and the response body is closed automatically.
	Body []byte

	// ProxyUsed is the URL of the proxy that handled this request.
	// Useful for debugging and monitoring per-proxy performance.
	// Format: "http://host:port"
	ProxyUsed string

	// Latency is the total time from request submission to result delivery.
	// Includes queue time, connection establishment, request execution, and response reading.
	Latency time.Duration

	// Error is the request-level error if the request failed.
	// Nil if the request succeeded (check Response.StatusCode for HTTP errors).
	// Common errors: context.DeadlineExceeded, network errors, circuit breaker open.
	Error error
}
