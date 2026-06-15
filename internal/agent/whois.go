package agent

import (
	"context"
	"net/http"
)

type ctxKey int

const callerKey ctxKey = 0

// callerFrom returns the tailnet caller's MagicDNS name recorded by whoisGuard (for audit).
func callerFrom(ctx context.Context) string {
	s, _ := ctx.Value(callerKey).(string)
	return s
}

// whoisGuard is the app-layer complement to the Tailscale ACL (defense in depth for
// hard constraint #1/#2): it asserts the caller carries tag:ci. It FAILS CLOSED — any
// WhoIs error or a missing tag rejects the request. On success it records the caller name
// in the request context for the audit log.
func (a *Agent) whoisGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lc, err := a.ts.LocalClient()
		if err != nil {
			a.deny(w, r, "localclient unavailable")
			return
		}
		who, err := lc.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil || who == nil || who.Node == nil || !hasTag(who.Node.Tags, "tag:ci") {
			a.deny(w, r, "caller is not tag:ci")
			return
		}
		ctx := context.WithValue(r.Context(), callerKey, who.Node.Name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Agent) deny(w http.ResponseWriter, r *http.Request, reason string) {
	a.log.Warn("rejected caller", "remote", r.RemoteAddr, "reason", reason)
	http.Error(w, "forbidden", http.StatusForbidden)
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
