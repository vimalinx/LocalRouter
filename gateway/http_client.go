package main

import (
	"errors"
	"net/http"
)

// Credentialed requests must stay on their operator-selected target. In
// particular, Go's default redirect policy forwards custom API-key headers.
func rejectUpstreamRedirect(_ *http.Request, _ []*http.Request) error {
	return errors.New("upstream redirects are not allowed")
}
