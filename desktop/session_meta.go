package main

// SessionMeta summarises one saved session for the history panel.
type SessionMeta struct {
	Path           string `json:"path"`
	SessionID      string `json:"sessionId,omitempty"`
	HostID         string `json:"hostId,omitempty"`
	Codec          string `json:"codec,omitempty"`
	Error          string `json:"error,omitempty"`
	Preview        string `json:"preview"`         // first user message
	Title          string `json:"title,omitempty"` // user-chosen name, when set (overrides preview)
	Turns          int    `json:"turns"`
	TurnsState     string `json:"turnsState"`
	CreatedAt      int64  `json:"createdAt"`      // unix milliseconds
	LastActivityAt int64  `json:"lastActivityAt"` // unix milliseconds
	DeletedAt      int64  `json:"deletedAt,omitempty"`
	Current        bool   `json:"current"`
	Open           bool   `json:"open"`
	Scope          string `json:"scope,omitempty"`
	WorkspaceRoot  string `json:"workspaceRoot,omitempty"`
	TopicID        string `json:"topicId,omitempty"`
	TopicTitle     string `json:"topicTitle,omitempty"`
	Kind           string `json:"kind,omitempty"` // "channel" for external IM transcripts
	Channel        string `json:"channel,omitempty"`
	ChannelLabel   string `json:"channelLabel,omitempty"`
	RemoteID       string `json:"remoteId,omitempty"`
	ChatType       string `json:"chatType,omitempty"`
	UserID         string `json:"userId,omitempty"`
	ThreadID       string `json:"threadId,omitempty"`
	SessionSource  string `json:"sessionSource,omitempty"`
}
