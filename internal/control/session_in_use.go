package control

// SessionLeaseCloseHint explains how to release a current session writer.
const SessionLeaseCloseHint = "close the other Deepzora window or process first"

// SessionInUseMessage is path-free because current writer locks do not expose
// another process's identity or filesystem location.
const SessionInUseMessage = "this session is in use by another Deepzora window or process"
