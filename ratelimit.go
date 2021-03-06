// Package ratelimit provides an efficient token bucket implementation
// that can be used to limit the rate concurrent HTTP traffic.
// See http://en.wikipedia.org/wiki/Token_bucket.
package ratelimit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/juju/ratelimit"
	"gopkg.in/vinxi/layer.v0"
)

// Filter represents the Limiter filter function signature.
type Filter func(r *http.Request) bool

// Exception represents the Limiter exception function signature.
type Exception Filter

// RateLimitResponder is used as default function to repond when the
// rate limit is reached. You can customize it via Limiter.SetResponder(fn).
var RateLimitResponder = func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(429)
	w.Write([]byte("Too Many Requests"))
}

// Limiter implements a token bucket rate limiter middleware.
// Rate limiter can support multiple rate limit strategies, such as time based limiter.
type Limiter struct {
	// bucket stores the ratelimit.Bucket limiter currently used.
	bucket *ratelimit.Bucket
	// responser stores the responder function used when the rate limit is reached.
	responder http.HandlerFunc
	// filters stores a list of filters to determine if should apply the rate limiter.
	filters []Filter
	// exceptions stores a list of exceptions to determine if should not apply the rate limiter.
	exceptions []Exception
}

// NewTimeLimiter creates a new time based rate limiter middleware.
func NewTimeLimiter(timeWindow time.Duration, capacity int64) *Limiter {
	return &Limiter{
		responder: RateLimitResponder,
		bucket:    ratelimit.NewBucket(timeWindow, capacity),
	}
}

// NewRateLimiter creates a rate limiter middleware which limits the
// amount of requests accepted per second.
func NewRateLimiter(rate float64, capacity int64) *Limiter {
	return &Limiter{
		responder: RateLimitResponder,
		bucket:    ratelimit.NewBucketWithRate(rate, capacity),
	}
}

// SetResponder sets a custom function to reply in case of rate limit reached.
func (l *Limiter) SetResponder(fn http.HandlerFunc) {
	l.responder = fn
}

// Filter registers a new rate limiter whitelist filter.
// If the filter matches, the traffic won't be limited.
func (l *Limiter) Filter(fn ...Filter) {
	l.filters = append(l.filters, fn...)
}

// Exception registers whitelist exception.
// If the exception function matches, the traffic won't be limited.
func (l *Limiter) Exception(fn ...Exception) {
	l.exceptions = append(l.exceptions, fn...)
}

// Register registers the middleware handler.
func (l *Limiter) Register(mw layer.Middleware) {
	mw.UsePriority("request", layer.TopHead, l.LimitHTTP)
}

// LimitHTTP limits an incoming HTTP request.
// If some filter passes, the request won't be limited.
// This method is used internally, but made public for public testing.
func (l *Limiter) LimitHTTP(h http.Handler) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Run exceptions to ignore the limiter, if necessary
		for _, exception := range l.exceptions {
			if exception(r) {
				h.ServeHTTP(w, r)
				return
			}
		}

		// Pass filters to determine if should apply the limiter.
		// All the filtes must pass to apply the limiter.
		for _, filter := range l.filters {
			if !filter(r) {
				h.ServeHTTP(w, r)
				return
			}
		}

		// Apply the rate limiter
		l.limit(w, r, h)
	}
}

// limit applies the rate limiter to the given HTTP request.
// If the rate exceeds, will reply with an error.
func (l *Limiter) limit(w http.ResponseWriter, r *http.Request, h http.Handler) {
	available := l.bucket.TakeAvailable(1)

	headers := w.Header()
	headers.Set("X-RateLimit-Limit", strconv.Itoa(l.capacity()))
	headers.Set("X-RateLimit-Remaining", strconv.Itoa(l.remaining()))

	// If tokens are not available, reply with error, usually with 429
	if available == 0 {
		l.responder(w, r)
		return
	}

	// Otherwise track time and forward the request
	h.ServeHTTP(w, r)
}

// capacity is used to read the current bucket capacity.
func (l *Limiter) capacity() int {
	return int(l.bucket.Capacity())
}

// remaining is used to read the current pending remaining available buckets.
func (l *Limiter) remaining() int {
	if remaining := int(l.bucket.Available()); remaining > 0 {
		return remaining
	}
	return 0
}
