package control

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"

	"deepzora/internal/attachment"
	"deepzora/internal/sessioncontent"
)

var hostVariantCache = attachment.NewVariantCache(attachment.DefaultCacheBytes, attachment.DefaultTransforms)

type controllerAttachmentState struct {
	attachments          atomic.Pointer[attachment.Service]
	attachmentOwnerMu    sync.Mutex
	attachmentOwnerScope string
	imageRoutesMu        sync.Mutex
	imageRoutesReady     bool
	imageRoutes          map[string]ImageRequestRoute
}

func (c *Controller) AttachmentOwnerIdentity() string {
	ref, _ := c.SessionRef()
	return ref.HostID + "\x00" + c.attachmentScope()
}

// NewAttachmentOperationContext binds an operation to both caller and owner.
func (c *Controller) NewAttachmentOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(c.attachmentContext(), cancel)
	return ctx, func() { stop(); cancel() }
}

func (c *Controller) attachmentContext() context.Context {
	if c.goalDriverControl.ctx != nil {
		return c.goalDriverControl.ctx
	}
	return context.Background()
}

func (c *Controller) attachmentService() *attachment.Service {
	if c == nil {
		return nil
	}
	return c.attachments.Load()
}

// Only the session publication owner changes this binding. Request goroutines
// retain their service snapshot; they never lazily choose another content root.
func (c *Controller) bindAttachmentService() {
	c.attachmentOwnerMu.Lock()
	defer c.attachmentOwnerMu.Unlock()
	content := c.sessionContentStore()
	scope := c.attachmentScope()
	current := c.attachments.Load()
	if current != nil && c.attachmentOwnerScope != scope {
		current.Drafts().ReleaseScope(c.attachmentOwnerScope)
	}
	c.attachmentOwnerScope = scope
	if current != nil && current.Store().Root() == content.Root() {
		return
	}
	c.attachments.Store(attachment.NewService(content, hostVariantCache))
}

func (c *Controller) sessionContentStore() *sessioncontent.Store {
	if c == nil {
		return nil
	}
	if service, runtime, current := c.v3Binding(); current {
		if runtime != nil {
			return runtime.Session().ContentStore()
		}
		// Attachments may be staged before the first turn allocates a session.
		// Keep them in that owner's current shared content root so publication
		// does not switch from a legacy SessionDir to a different object store.
		if service != nil && service.Query().StorageRoot() != "" {
			return sessioncontent.New(filepath.Join(service.Query().StorageRoot(), ".content-v1"))
		}
		return nil
	}
	if store := c.sessionEventStore(); store != nil {
		if content := store.ContentStore(); content != nil {
			return content
		}
	}
	if c.sessionDir != "" {
		return sessioncontent.New(filepath.Join(c.sessionDir, ".content-v1"))
	}
	if c.workspaceRoot != "" {
		return sessioncontent.New(filepath.Join(c.workspaceRoot, ".deepzora", "content-v1"))
	}
	return nil
}

func (c *Controller) attachmentScope() string {
	if c == nil {
		return ""
	}
	if _, runtime, _ := c.v3Binding(); runtime != nil {
		store := runtime.Session()
		return store.ID() + ":" + store.StorageGeneration()
	}
	if store := c.sessionEventStore(); store != nil {
		return store.ID() + ":" + store.StorageGeneration()
	}
	return ""
}
