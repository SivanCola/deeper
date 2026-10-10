package permission

import "deeper/internal/shellsafe"

func normalizeBashSafeRedirectsForMatch(subject string) (string, bool) {
	return shellsafe.NormalizeBashSafeRedirectsForMatch(subject)
}
