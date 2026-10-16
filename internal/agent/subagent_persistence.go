package agent

import (
	"context"

	"deepzora/internal/provider"
)

// SubagentPersistence is supplied by the owning host. Transcripts use its
// current session catalog; the Agent package never resolves historical paths.
type SubagentPersistence interface {
	Root() string
	Create(context.Context, SubagentMeta, []provider.Message) (SubagentTranscript, error)
	Open(context.Context, string) (SubagentTranscript, error)
	Copy(context.Context, string, SubagentMeta) (SubagentTranscript, error)
	Read(context.Context, string) ([]provider.Message, error)
	Delete(context.Context, string) error
}

// SubagentTranscript records exact commits from a child Agent. Messages is the
// provider context for continuation; Read on the catalog returns full history,
// including messages no longer present in that context after compaction.
type SubagentTranscript interface {
	SessionCheckpointer
	SessionEventRecorder
	SessionMessageMutationRecorder
	SessionModelContextRecorder
	Messages() []provider.Message
	Flush(context.Context) error
	Close(context.Context) error
}
