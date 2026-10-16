package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"deepzora/internal/control"
	"deepzora/internal/event"
)

// TurnStartView is the synchronous admission receipt for the new Wails turn
// API. Events remain the streaming authority after admission.
type TurnStartView struct {
	TurnID      string                    `json:"turnId"`
	Status      event.TurnStatus          `json:"status"`
	Disposition control.SubmitDisposition `json:"disposition"`
	OperationID string                    `json:"operationId,omitempty"`
	// A management refusal is correlated with its existing operation, not a failed chat turn.
	ManagementErrorCode string `json:"managementErrorCode,omitempty"`
	RuntimeEpoch        string `json:"runtimeEpoch,omitempty"`
	SubmissionID        string `json:"submissionId,omitempty"`
}

// stoppableCtrl resolves and captures the controller a Stop request targets.
// The request is session-scoped and idle cancellation is idempotent.
func (a *App) stoppableCtrl(tabID, turnID string) (control.SessionAPI, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return nil, a.workspaceNotReadyErr(tab)
	}
	// Logging must never refresh execution state before delivering Stop. An
	// empty ID is the ordinary session-scoped command, not a stale turn.
	if reader, ok := ctrl.(control.PublishedRuntimeStateReader); ok && strings.TrimSpace(turnID) != "" {
		state := reader.PublishedRuntimeStateSnapshot()
		if turnID = strings.TrimSpace(turnID); turnID != state.TurnID {
			slog.Info("desktop: stop targeted a stale turn id; interrupting the active turn", "tab", tabID, "requested", turnID, "active", state.TurnID)
		}
	}
	return ctrl, nil
}

// CancelSessionForTab is the protocol-v2 Stop operation. It captures the tab's
// current controller exactly once, so later tab switches cannot retarget it.
func (a *App) CancelSessionForTab(tabID string) (control.CancelReceipt, error) {
	ctrl, err := a.stoppableCtrl(tabID, "")
	if err != nil {
		return control.CancelReceipt{}, err
	}
	return ctrl.CancelSessionFrom("user_stop"), nil
}

// StartTurnForTab returns the admitted turn identity and management receipt.
func (a *App) StartTurnForTab(tabID, input, submissionID string) (TurnStartView, error) {
	if err := validateTurnSubmissionIdentity(tabID, submissionID); err != nil {
		return TurnStartView{}, err
	}
	if _, ctrl := a.tabAndCtrlByID(tabID); ctrl != nil {
		if identified, ok := ctrl.(*control.Controller); ok {
			receipt, found, err := identified.LookupSubmission(control.SubmissionRequest{ID: submissionID, Input: input, Display: input})
			if err != nil {
				return TurnStartView{}, err
			}
			if found {
				return TurnStartView{TurnID: receipt.TurnID, Status: event.TurnQueued, Disposition: control.SubmitTurnStarted, SubmissionID: submissionID}, nil
			}
		}
	}
	result, err := a.submitToTabResult(tabID, input, true, submissionID)
	if err != nil {
		if result.Disposition == control.SubmitManagementHandled && result.OperationID != "" {
			code := ""
			switch {
			case errors.Is(err, control.ErrMaintenanceBusy):
				code = "maintenance_busy"
			case errors.Is(err, control.ErrMaintenanceRecovery):
				code = "maintenance_recovery_required"
			}
			if code != "" {
				return TurnStartView{Disposition: result.Disposition, OperationID: result.OperationID,
					SubmissionID: submissionID, ManagementErrorCode: code}, nil
			}
		}
		return TurnStartView{}, err
	}
	if result.Disposition == control.SubmitManagementHandled {
		return TurnStartView{Disposition: result.Disposition, OperationID: result.OperationID, SubmissionID: submissionID}, nil
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return TurnStartView{}, a.workspaceNotReadyErr(tab)
	}
	// Admission is released by now and the tab may hold a rebuilt controller,
	// so the admitting controller's receipt is the authority when it has one.
	turnID := result.TurnID
	if admitted, ok := ctrl.(interface{ TurnIDForSubmission(string) string }); ok && turnID == "" {
		turnID = admitted.TurnIDForSubmission(submissionID)
	}
	if strings.TrimSpace(turnID) == "" {
		return TurnStartView{}, fmt.Errorf("turn admission did not produce a durable turn id")
	}
	epoch := ""
	if tab != nil && tab.sink != nil {
		epoch = tab.sink.runtimeEpochSnapshot()
	}
	// This is an admission receipt, not a potentially raced runtime snapshot.
	// Ordered events carry every later transition, including a provider that
	// completed before the Wails Promise was delivered.
	return TurnStartView{TurnID: turnID, Status: event.TurnQueued, Disposition: control.SubmitTurnStarted, RuntimeEpoch: epoch, SubmissionID: submissionID}, nil
}

func (a *App) StartTurnForTabWithDrafts(tabID, input, submissionID string, draftIDs []string) (TurnStartView, error) {
	if err := validateTurnSubmissionIdentity(tabID, submissionID); err != nil {
		return TurnStartView{}, err
	}
	req := control.SubmissionRequest{ID: submissionID, Input: input, Display: input, DraftIDs: append([]string(nil), draftIDs...)}
	if _, ctrl := a.tabAndCtrlByID(tabID); ctrl != nil {
		if identified, ok := ctrl.(*control.Controller); ok {
			receipt, found, err := identified.LookupSubmission(req)
			if err != nil {
				return TurnStartView{}, err
			}
			if found {
				return TurnStartView{TurnID: receipt.TurnID, Status: event.TurnQueued, Disposition: control.SubmitTurnStarted, SubmissionID: submissionID}, nil
			}
		}
	}
	admission, ctrl, err := a.beginTabTurn(tabID, true, submissionID)
	if err != nil {
		return TurnStartView{}, a.submissionAdmissionError(tabID, req, err)
	}
	defer admission.abort()
	if err := a.ensureTabTopicIndexedForUserTurn(admission.tab); err != nil {
		return TurnStartView{}, err
	}
	identified, ok := ctrl.(*control.Controller)
	if !ok {
		return TurnStartView{}, fmt.Errorf("unsupported: attachments-v1")
	}
	receipt, err := identified.SubmitIdentified(req)
	if err != nil {
		return TurnStartView{}, inboxBridgeError(err)
	}
	admission.finish(ctrl)
	turnID := receipt.TurnID
	if strings.TrimSpace(turnID) == "" {
		return TurnStartView{}, fmt.Errorf("turn admission did not produce a durable turn id")
	}
	epoch := ""
	if admission.tab != nil && admission.tab.sink != nil {
		epoch = admission.tab.sink.runtimeEpochSnapshot()
	}
	return TurnStartView{TurnID: turnID, Status: event.TurnQueued, Disposition: control.SubmitTurnStarted, RuntimeEpoch: epoch, SubmissionID: submissionID}, nil
}

// InterruptTurnForTab stops the tab's active work. Stop is a session-level
// request: a turn id from a stale button still interrupts whatever is running
// now, because an unstoppable turn is worse than stopping its replacement.
func (a *App) InterruptTurnForTab(tabID, turnID string) error {
	ctrl, err := a.stoppableCtrl(tabID, turnID)
	if err != nil {
		return err
	}
	if session, ok := ctrl.(interface{ CancelSession() control.CancelReceipt }); ok {
		session.CancelSession()
	} else {
		ctrl.Cancel()
	}
	return nil
}

// InterruptTurnWithInboxItemsForTab is the receipt-capable Stop used by the
// Composer when it also discards queued follow-ups.
func (a *App) InterruptTurnWithInboxItemsForTab(tabID, turnID string, itemIDs []string) (InboxCancelResultView, error) {
	view := InboxCancelResultView{DiscardedItemIDs: []string{}}
	ctrl, err := a.stoppableCtrl(tabID, turnID)
	if err != nil {
		return view, err
	}
	result, err := ctrl.CancelWithInboxItemsResult(itemIDs, "desktop")
	if err != nil {
		return view, inboxBridgeError(err)
	}
	view.DiscardedItemIDs = append(view.DiscardedItemIDs, result.DiscardedItemIDs...)
	view.Warning = result.Warning
	a.emitInboxChanged(tabID)
	return view, nil
}

var _ control.SessionAPI = (*control.Controller)(nil)
