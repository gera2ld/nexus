package auth

import (
	"sync"
	"time"
)

// Limiter blocks a client after too many failed sign-ins in a window.
// It is in-memory and per process, which is enough for a single binary.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	seen   map[string]*failures
}

type failures struct {
	count int
	since time.Time
}

// maxTracked bounds memory if many addresses fail.
const maxTracked = 10000

// NewLimiter allows max failures per client within window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, seen: map[string]*failures{}}
}

// Blocked reports whether ip has used up its failures for this window.
func (l *Limiter) Blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.current(ip)
	return f != nil && f.count >= l.max
}

// Fail records a failed sign-in from ip.
func (l *Limiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.current(ip)
	if f == nil {
		if len(l.seen) >= maxTracked {
			l.sweep()
		}
		f = &failures{since: l.now()}
		l.seen[ip] = f
	}
	f.count++
}

// current returns ip's entry if it is still inside the window.
func (l *Limiter) current(ip string) *failures {
	f := l.seen[ip]
	if f != nil && l.now().Sub(f.since) >= l.window {
		delete(l.seen, ip)
		return nil
	}
	return f
}

func (l *Limiter) sweep() {
	for ip, f := range l.seen {
		if l.now().Sub(f.since) >= l.window {
			delete(l.seen, ip)
		}
	}
	if len(l.seen) >= maxTracked { // still full: drop everything rather than grow
		l.seen = map[string]*failures{}
	}
}
