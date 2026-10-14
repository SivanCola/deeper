package cli

import (
	"net/url"
)

// Browser authentication always uses the current fragment bootstrap.
func remoteServeBrowserURL(bound, token string) string {
	base := "http://" + bound + "/"
	escapedToken := url.QueryEscape(token)
	return base + "#token=" + escapedToken
}
