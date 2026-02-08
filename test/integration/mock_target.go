package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

// MockTarget represents a mock API endpoint for testing
type MockTarget struct {
	Server       *httptest.Server
	URL          string
	RequestCount atomic.Int64
}

// NewMockTarget creates a new mock target API server
func NewMockTarget() *MockTarget {
	mt := &MockTarget{}

	// Create HTTP handler
	mux := http.NewServeMux()

	// Simple GET endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mt.RequestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ok","method":"%s","path":"%s","timestamp":"%s"}`,
			r.Method, r.URL.Path, time.Now().Format(time.RFC3339))
	})

	// Data endpoint
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		mt.RequestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"test data","id":1}`))
	})

	// Item endpoint with ID
	mux.HandleFunc("/item/", func(w http.ResponseWriter, r *http.Request) {
		mt.RequestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"item":"%s","status":"ok"}`, r.URL.Path)
	})

	// Slow endpoint
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		mt.RequestCount.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"slow response"}`))
	})

	// Error endpoint (returns 500)
	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		mt.RequestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	})

	// Create server
	mt.Server = httptest.NewServer(mux)
	mt.URL = mt.Server.URL

	return mt
}

// Stop stops the mock target server
func (mt *MockTarget) Stop() {
	if mt.Server != nil {
		mt.Server.Close()
	}
}

// GetRequestCount returns the total number of requests received
func (mt *MockTarget) GetRequestCount() int64 {
	return mt.RequestCount.Load()
}

// ResetCount resets the request counter
func (mt *MockTarget) ResetCount() {
	mt.RequestCount.Store(0)
}
