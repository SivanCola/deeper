package provider

import "time"

// DefaultStreamIdleTimeout caps how long a started SSE stream may go silent
// before the client treats it as a dropped connection. A half-open TCP
// connection (e.g. a proxy switched mid-stream) sends no RST, so scanning would
// block forever; this turns that hang into a recoverable error. Generous on
// purpose — live streams emit tokens/keepalives far more often.
const DefaultStreamIdleTimeout = 300 * time.Second
