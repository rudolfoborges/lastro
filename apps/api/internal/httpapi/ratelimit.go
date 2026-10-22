package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	limiterIdleTTL       = 10 * time.Minute
	limiterSweepInterval = time.Minute
)

type limiterSet struct {
	mu        sync.Mutex
	limit     rate.Limit
	burst     int
	entries   map[string]*limiterEntry
	lastSweep time.Time
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newLimiterSet(perMinute int) *limiterSet {
	set := &limiterSet{limit: rate.Inf, entries: map[string]*limiterEntry{}, lastSweep: time.Now()}
	if perMinute > 0 {
		set.limit = rate.Every(time.Minute / time.Duration(perMinute))
		set.burst = perMinute
	}
	return set
}

func (s *limiterSet) allow(key string) bool {
	return s.entry(key).Allow()
}

func (s *limiterSet) exhausted(key string) bool {
	return s.limit != rate.Inf && s.entry(key).Tokens() < 1
}

func (s *limiterSet) retryAfterSeconds() string {
	if s.limit == rate.Inf {
		return "1"
	}
	return strconv.Itoa(int(math.Ceil(1 / float64(s.limit))))
}

func (s *limiterSet) entry(key string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.lastSweep) > limiterSweepInterval {
		for k, e := range s.entries {
			if now.Sub(e.lastSeen) > limiterIdleTTL {
				delete(s.entries, k)
			}
		}
		s.lastSweep = now
	}
	e, ok := s.entries[key]
	if !ok {
		e = &limiterEntry{limiter: rate.NewLimiter(s.limit, s.burst)}
		s.entries[key] = e
	}
	e.lastSeen = now
	return e.limiter
}

func writeRateLimited(w http.ResponseWriter, limits *limiterSet) {
	w.Header().Set("Retry-After", limits.retryAfterSeconds())
	writeError(w, http.StatusTooManyRequests, "rate_limited")
}

func clientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			hops := strings.Split(forwarded, ",")
			return strings.TrimSpace(hops[len(hops)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
