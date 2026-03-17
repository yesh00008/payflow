package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents circuit breaker state
type State int

const (
	Closed   State = iota // Normal operation
	Open                  // Failing, reject calls
	HalfOpen              // Testing recovery
)

var (
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

// CircuitBreaker implements the circuit breaker pattern to prevent
// cascading failures between microservices.
type CircuitBreaker struct {
	mu               sync.RWMutex
	name             string
	state            State
	failureCount     int
	successCount     int
	failureThreshold int
	successThreshold int
	timeout          time.Duration
	lastFailure      time.Time
	onStateChange    func(name string, from, to State)
}

// Config holds circuit breaker configuration
type Config struct {
	Name             string
	FailureThreshold int           // Open after N consecutive failures (default: 5)
	SuccessThreshold int           // Close after N successes in HalfOpen (default: 3)
	Timeout          time.Duration // Wait before trying HalfOpen (default: 60s)
	OnStateChange    func(name string, from, to State)
}

// New creates a new circuit breaker with the given config
func New(cfg Config) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &CircuitBreaker{
		name:             cfg.Name,
		state:            Closed,
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		timeout:          cfg.Timeout,
		onStateChange:    cfg.OnStateChange,
	}
}

// Execute runs the given function through the circuit breaker.
// If the circuit is open, it returns ErrCircuitOpen immediately.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if !cb.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case Closed:
		return true
	case Open:
		// Check if timeout has elapsed → transition to HalfOpen
		if time.Since(cb.lastFailure) > cb.timeout {
			cb.mu.RUnlock()
			cb.setState(HalfOpen)
			cb.mu.RLock()
			return true
		}
		return false
	case HalfOpen:
		return true
	}
	return false
}

func (cb *CircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	if cb.state == HalfOpen {
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.mu.Unlock()
			cb.setState(Closed)
			cb.mu.Lock()
			cb.successCount = 0
		}
	}
}

func (cb *CircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailure = time.Now()
	cb.successCount = 0

	if cb.failureCount >= cb.failureThreshold {
		cb.mu.Unlock()
		cb.setState(Open)
		cb.mu.Lock()
	}
}

func (cb *CircuitBreaker) setState(newState State) {
	cb.mu.Lock()
	old := cb.state
	cb.state = newState
	cb.mu.Unlock()

	if cb.onStateChange != nil && old != newState {
		cb.onStateChange(cb.name, old, newState)
	}
}

// State returns the current circuit breaker state
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	}
	return "unknown"
}
