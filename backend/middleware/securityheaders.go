package middleware

import (
	"github.com/pocketbase/pocketbase/core"
)

// SecurityHeadersMiddleware sets security-related HTTP headers on all responses.
func SecurityHeadersMiddleware(e *core.RequestEvent) error {
	h := e.Response.Header()

	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	h.Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self' https://static.cloudflareinsights.com 'sha256-/b6J8yVhTpF8Br3QshZ+JwWygKpV7spNgjCTx2Aj0EE='; "+
			"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
			"font-src 'self' https://fonts.gstatic.com; "+
			"img-src 'self' data:; "+
			"connect-src 'self' https://api.github.com https://cloudflareinsights.com; "+
			"frame-ancestors 'none'; "+
			"base-uri 'self'; "+
			"form-action 'self'",
	)

	return e.Next()
}
