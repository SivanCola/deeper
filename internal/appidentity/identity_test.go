package appidentity

import (
	"strings"
	"testing"
)

func TestAppUserModelIDIsStableAndVersionIndependent(t *testing.T) {
	if AppUserModelID != "com.deepzora.desktop" {
		t.Fatalf("AppUserModelID = %q, want stable desktop identity %q", AppUserModelID, "com.deepzora.desktop")
	}
	if strings.ContainsAny(AppUserModelID, " \t\r\n") || len(AppUserModelID) > 128 {
		t.Fatalf("invalid AppUserModelID %q", AppUserModelID)
	}
}
