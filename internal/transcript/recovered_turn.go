package transcript

// RecoveredDisplayTurn carries current retained events reconstructed after a crash.
// The canonical user-message identity binds every recovered presentation row.
type RecoveredDisplayTurn struct {
	TurnID        string
	UserMessageID string
	Messages      []Message
}
