package smsprovider

import (
	"net/http"
	"regexp"
)

// InstallSNSTestOverrides swaps the HTTP client and host regex used by SNS
// signature verification and empties the cert cache. The returned function
// reverses every change and is meant to be handed to t.Cleanup. Tests-only.
//
// Splitting the install + restore was tempting but kept four exported
// mutators in the package; this collapses to one entry point.
func InstallSNSTestOverrides(client *http.Client, hostRe *regexp.Regexp) (restore func()) {
	oldClient := httpClient
	httpClient = client

	oldRe := awsSNSHostRe
	awsSNSHostRe = hostRe

	certCacheLock.Lock()
	oldCache := certCache
	certCache = make(map[string]cachedCert)
	certCacheLock.Unlock()

	return func() {
		httpClient = oldClient
		awsSNSHostRe = oldRe
		certCacheLock.Lock()
		certCache = oldCache
		certCacheLock.Unlock()
	}
}

// BuildCanonical exposes buildCanonicalString to tests in other packages that
// need to compute a deterministic byte sequence to sign. Tests-only.
func BuildCanonical(msg *SNSMessage) (string, error) {
	return buildCanonicalString(msg)
}
