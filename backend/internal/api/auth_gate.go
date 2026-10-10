package api

import (
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"event-driven-context/internal/core"
)

const (
	authBurst   = 20
	authMaxKeys = 10000
	authIdleTTL = 10 * time.Minute
)

// All gates share a process-wide bound on concurrent password hashing work.
var authSlots = make(chan struct{}, 4)

type authBucket struct {
	key    string
	tokens float64
	last   time.Time
}

type authGate struct {
	mu             sync.Mutex
	buckets        map[string]*list.Element
	recent         list.List
	trustedProxies []netip.Prefix
}

func newAuthGate(trustedProxies []netip.Prefix) *authGate {
	return &authGate{buckets: make(map[string]*list.Element), trustedProxies: trustedProxies}
}

// allow evicts idle keys lazily and the least recently used key at capacity.
// The list keeps cleanup proportional to evictions, not the size of the map.
// With debit false, it checks the budget without consuming a token.
func (g *authGate) allow(key string, now time.Time, debit bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for e := g.recent.Back(); e != nil; e = g.recent.Back() {
		if now.Sub(e.Value.(*authBucket).last) < authIdleTTL {
			break
		}
		g.remove(e)
	}
	e := g.buckets[key]
	if e == nil {
		if len(g.buckets) >= authMaxKeys {
			g.remove(g.recent.Back())
		}
		e = g.recent.PushFront(&authBucket{key: key, tokens: authBurst, last: now})
		g.buckets[key] = e
	}
	b := e.Value.(*authBucket)
	b.tokens = min(authBurst, b.tokens+now.Sub(b.last).Seconds()/2)
	b.last = now
	g.recent.MoveToFront(e)
	if b.tokens < 1 {
		return false
	}
	if debit {
		b.tokens--
	}
	return true
}

func (g *authGate) remove(e *list.Element) {
	delete(g.buckets, e.Value.(*authBucket).key)
	g.recent.Remove(e)
}

func (g *authGate) login(fn func(context.Context, core.Credentials) (core.LoginResult, error)) http.Handler {
	return g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in core.Credentials
		if err := decode(r, &in); err != nil {
			fail(w, err)
			return
		}
		// Match core.Store.Login normalization. Hash to bound retained key size.
		username := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(in.Username))))
		key := fmt.Sprintf("user:%x", username)
		if !g.allow(key, time.Now(), false) {
			authRateLimited(w, "too many authentication requests")
			return
		}
		out, err := fn(r.Context(), in)
		if err != nil {
			var authErr *core.Error
			if errors.As(err, &authErr) && authErr.Code == "unauthenticated" {
				g.allow(key, time.Now(), true)
			}
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	}))
}

func (g *authGate) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.allow("ip:"+g.clientIP(r).String(), time.Now(), true) {
			authRateLimited(w, "too many authentication requests")
			return
		}
		select {
		case authSlots <- struct{}{}:
			defer func() { <-authSlots }()
			h.ServeHTTP(w, r)
		default:
			authRateLimited(w, "authentication busy")
		}
	})
}

func authRateLimited(w http.ResponseWriter, message string) {
	w.Header().Set("Retry-After", "2")
	respond(w, http.StatusTooManyRequests, map[string]any{"error": core.Error{Code: "rate_limited", Message: message}})
}

func (g *authGate) trusted(ip netip.Addr) bool {
	for _, prefix := range g.trustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (g *authGate) clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	peer = peer.Unmap()
	if !g.trusted(peer) {
		return peer
	}
	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		hops := strings.Split(strings.Join(values, ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			// A malformed chain must not let the caller choose an earlier hop.
			if err != nil {
				return peer
			}
			ip = ip.Unmap()
			if !g.trusted(ip) || i == 0 {
				return ip
			}
		}
		return peer
	}
	// X-Real-IP is only a fallback when no forwarded chain was supplied.
	if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return ip.Unmap()
	}
	return peer
}
