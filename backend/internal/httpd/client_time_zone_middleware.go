package httpd

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
)

// clientTimeZoneMiddleware records the time zone the desktop app reports, so
// agent prompts name the user's zone rather than this daemon's, which differs
// on a remote host. It never rejects a request; a missing or invalid header
// is ignored, and so is any LAN request that did not authenticate as a
// trusted client (the identity probe, a pairing-code request).
func clientTimeZoneMiddleware(rec *clienttz.Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if rec == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if zone := r.Header.Get(clienttz.Header); zone != "" && trustedTimeZoneReporter(r) {
				rec.Observe(r.Context(), zone)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// trustedTimeZoneReporter accepts loopback requests (the local desktop app).
// Fork: the daemon is loopback-only, and the LAN credential kinds the upstream
// check consults do not exist here — a non-loopback caller is simply refused.
func trustedTimeZoneReporter(r *http.Request) bool {
	return !requestscope.IsLAN(r.Context())
}
