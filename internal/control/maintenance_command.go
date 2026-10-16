package control

import "strings"

// MaintenanceCommandConflict lets transports preserve the management receipt
// when duplicate compaction is rejected before ordinary input admission.
func MaintenanceCommandConflict(ctrl SessionAPI, input string) (SubmitResult, error) {
	if input != "/compact" && !strings.HasPrefix(input, "/compact ") {
		return SubmitResult{}, nil
	}
	if ctrl == nil {
		return SubmitResult{}, nil
	}
	maintenance := ctrl.RuntimeStateSnapshot().Maintenance
	if maintenance == nil {
		return SubmitResult{}, nil
	}
	err := ErrMaintenanceBusy
	if maintenance.Activity == "recovery_required" {
		err = ErrMaintenanceRecovery
	}
	return SubmitResult{Disposition: SubmitManagementHandled, OperationID: maintenance.OperationID}, err
}
