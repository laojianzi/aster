package kube

import (
	"errors"
	"net/http"
)

var ErrAPIRedirect = errors.New("API redirects are disabled; configure and trust the intended API endpoint explicitly")

// Stop redirects inside the authenticated transport, before net/http or a
// streaming dialer can reissue the request with the same credential wrapper.
// This also protects transports constructed later from Backend.Config().
// Neither the untrusted Location header nor response body is included in errors.
type rejectAPIRedirects struct{ base http.RoundTripper }

func (t rejectAPIRedirects) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if response != nil && response.StatusCode >= 300 && response.StatusCode < 400 {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, ErrAPIRedirect
	}
	return response, err
}
