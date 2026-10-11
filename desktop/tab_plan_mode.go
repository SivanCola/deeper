package main

import "deepzora/internal/control"

func applyTabModeToController(ctrl control.SessionAPI, mode string) []string {
	if ctrl != nil {
		ctrl.SetPlanMode(tabModeHasPlan(mode))
	}
	return nil
}
