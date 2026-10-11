package repair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/fileutil"
	filelock "deepzora/internal/identitylock"
)

// hashFile binds current bundle contents into the repair confirmation digest.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

const updateTransactionVersion = 1
const pendingUpdateLockTimeout = 5 * time.Second

var repairExecutable = os.Executable

type UpdateTransaction struct {
	SchemaVersion int    `json:"schemaVersion"`
	FromVersion   string `json:"fromVersion,omitempty"`
	ToVersion     string `json:"toVersion"`
	Platform      string `json:"platform"`
	TargetKind    string `json:"targetKind"` // app-bundle
	TargetPath    string `json:"targetPath"`
	BackupPath    string `json:"backupPath"`
	CreatedAt     string `json:"createdAt"`
	// Handoff fields authorize the detached macOS updater to act on paths
	// recorded by the live desktop process when detached handoff is selected.
	HandoffAppPath       string `json:"handoffAppPath,omitempty"`
	HandoffStagingPath   string `json:"handoffStagingPath,omitempty"`
	HandoffAppTreeID     string `json:"handoffAppTreeId,omitempty"`
	HandoffStagingTreeID string `json:"handoffStagingTreeId,omitempty"`
	HandoffOwnerPID      int    `json:"handoffOwnerPid,omitempty"`
	// BackupTreeID binds a macOS rollback backup to the bundle captured before
	// the update. Current app-bundle transactions require this digest.
	BackupTreeID string `json:"backupTreeId,omitempty"`
}

type UpdateRollbackResult struct {
	RolledBack  bool   `json:"rolledBack"`
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion,omitempty"`
	TargetPath  string `json:"targetPath,omitempty"`
	// MixedInstall reports that a failed rollback could not be compensated:
	// the install now mixes binaries from two releases. Launchers must not
	// start the desktop in this state.
	MixedInstall bool `json:"mixedInstall,omitempty"`
}

// ErrPendingUpdateAwaitingHealth reports that the currently running release is
// still the probationary target of a prior update. Callers must not cancel or
// roll it back merely to start another update; the normal startup health
// confirmation owns that transition.
var ErrPendingUpdateAwaitingHealth = errors.New("previous update is awaiting startup health confirmation")

var errPendingUpdateForeignInstall = errors.New("pending update belongs to a different installation")

// pendingUpdateHealthStaleAfter bounds how long Reconcile waits for startup
// health before auto-committing a still-running probationary target.
var pendingUpdateHealthStaleAfter = 24 * time.Hour

// PendingUpdateReconcileResult describes the safe transition performed before
// startup or a new install. Cleared = pre-publish cancel; RolledBack = verified
// restore; Healthy = probationary target committed after install evidence.
type PendingUpdateReconcileResult struct {
	Pending        bool   `json:"pending"`
	Cleared        bool   `json:"cleared,omitempty"`
	RolledBack     bool   `json:"rolledBack,omitempty"`
	MixedInstall   bool   `json:"mixedInstall,omitempty"`
	AwaitingHealth bool   `json:"awaitingHealth,omitempty"`
	Healthy        bool   `json:"healthy,omitempty"`
	FromVersion    string `json:"fromVersion,omitempty"`
	ToVersion      string `json:"toVersion,omitempty"`
	TargetPath     string `json:"targetPath,omitempty"`
}

// UpdateVersionsEqual reports whether two release version strings name the same
// release, normalizing an optional leading "v"/"V" prefix.
func UpdateVersionsEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return normalizeUpdateVersion(a) == normalizeUpdateVersion(b)
}

func normalizeUpdateVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") && !strings.HasPrefix(v, "V") {
		return "v" + v
	}
	return "v" + strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
}

// pendingUpdateHealthIsStaleOverride forces the stale decision in tests without
// rewriting CreatedAt (part of transaction identity).
var pendingUpdateHealthIsStaleOverride func(*UpdateTransaction) bool

func pendingUpdateHealthIsStale(tx *UpdateTransaction) bool {
	if tx == nil {
		return false
	}
	if pendingUpdateHealthIsStaleOverride != nil {
		return pendingUpdateHealthIsStaleOverride(tx)
	}
	if pendingUpdateHealthStaleAfter <= 0 {
		return false
	}
	created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(tx.CreatedAt))
	if err != nil {
		created, err = time.Parse(time.RFC3339, strings.TrimSpace(tx.CreatedAt))
	}
	if err != nil {
		return false
	}
	return time.Since(created) >= pendingUpdateHealthStaleAfter
}

// UpdateTransactionID returns a stable, opaque identity for the complete
// transaction. Platform handoff processes use it so copied scalar fields such
// as version and creation time cannot authorize a rewritten pending update.
func UpdateTransactionID(tx *UpdateTransaction) string {
	if tx == nil {
		return ""
	}
	return repairPlanStateID(tx)
}

func PendingUpdatePath() string {
	root := config.MemoryUserDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "repair", "pending-update.json")
}

// lockPendingUpdateStrict serializes cross-process pending-update transitions:
// prepare, rollback, commit, and cancel. Two launchers can run recovery at
// concurrently; a single restorer owns bundle publication and compensation.
func lockPendingUpdateStrict() (func(), error) {
	path := PendingUpdatePath()
	if path == "" {
		return nil, fmt.Errorf("pending update: Deepzora state directory is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pendingUpdateLockTimeout)
	defer cancel()
	unlock, err := filelock.Acquire(ctx, path+".lock")
	if err != nil {
		return nil, err
	}
	return unlock, nil
}

var acquirePendingUpdateLock = lockPendingUpdateStrict

// PrepareAppBundleUpdate records the sibling bundle backup that the macOS
// handoff script creates. The script performs the directory move after exit.
func PrepareAppBundleUpdate(fromVersion, toVersion, appPath, backupPath string) (*UpdateTransaction, error) {
	tx, err := newAppBundleUpdateTransaction(fromVersion, toVersion, appPath, backupPath)
	if err != nil {
		return nil, err
	}
	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return nil, fmt.Errorf("prepare update: lock pending transaction: %w", err)
	}
	defer unlock()
	if err := ensureNoPendingUpdate(); err != nil {
		return nil, err
	}
	unlockTargets, lockErr := lockRepairMutations(tx.TargetPath, tx.BackupPath)
	if lockErr != nil {
		return nil, fmt.Errorf("prepare update: lock targets: %w", lockErr)
	}
	defer unlockTargets()
	tx.BackupTreeID, err = repairPlanTreeContentStateID(tx.TargetPath)
	if err != nil {
		return nil, fmt.Errorf("prepare update: current bundle digest: %w", err)
	}
	if err := ensureNoPendingUpdate(); err != nil {
		return nil, err
	}
	if err := createPendingUpdate(tx); err != nil {
		return nil, err
	}
	return tx, nil
}

// PrepareAppBundleUpdateHandoff records every path the detached macOS updater
// may mutate. The child receives only the transaction identity and must claim
// these recorded paths under the pending-update and mutation locks.
func PrepareAppBundleUpdateHandoff(fromVersion, toVersion, appPath, backupPath, stagedAppPath, stagingPath string, ownerPID int) (*UpdateTransaction, error) {
	tx, err := newAppBundleUpdateTransaction(fromVersion, toVersion, appPath, backupPath)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(tx.TargetPath) {
		return nil, fmt.Errorf("prepare update: invalid macOS bundle paths")
	}
	tx.HandoffAppPath = filepath.Clean(strings.TrimSpace(stagedAppPath))
	tx.HandoffStagingPath = filepath.Clean(strings.TrimSpace(stagingPath))
	tx.HandoffOwnerPID = ownerPID
	if err := validateAppBundleHandoffMetadata(tx); err != nil {
		return nil, fmt.Errorf("prepare update: %w", err)
	}

	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return nil, fmt.Errorf("prepare update: lock pending transaction: %w", err)
	}
	defer unlock()
	if err := ensureNoPendingUpdate(); err != nil {
		return nil, err
	}
	unlockTargets, err := lockRepairMutations(tx.TargetPath, tx.BackupPath)
	if err != nil {
		return nil, fmt.Errorf("prepare update: lock targets: %w", err)
	}
	defer unlockTargets()
	tx.HandoffAppTreeID, err = repairPlanTreePayloadStateID(tx.HandoffAppPath)
	if err != nil {
		return nil, fmt.Errorf("prepare update: stage bundle digest: %w", err)
	}
	tx.HandoffStagingTreeID, err = repairPlanTreeContentStateID(tx.HandoffStagingPath)
	if err != nil {
		return nil, fmt.Errorf("prepare update: staging directory digest: %w", err)
	}
	tx.BackupTreeID, err = repairPlanTreeContentStateID(tx.TargetPath)
	if err != nil {
		return nil, fmt.Errorf("prepare update: current bundle digest: %w", err)
	}
	if err := VerifyAppBundleUpdateHandoffSource(tx); err != nil {
		return nil, fmt.Errorf("prepare update: %w", err)
	}
	if err := VerifyAppBundleUpdateHandoffOriginal(tx); err != nil {
		return nil, fmt.Errorf("prepare update: %w", err)
	}
	if err := verifyAppBundleUpdateHandoffBackupAbsent(tx); err != nil {
		return nil, fmt.Errorf("prepare update: %w", err)
	}
	if err := ensureNoPendingUpdate(); err != nil {
		return nil, err
	}
	if err := createPendingUpdate(tx); err != nil {
		return nil, err
	}
	return tx, nil
}

func newAppBundleUpdateTransaction(fromVersion, toVersion, appPath, backupPath string) (*UpdateTransaction, error) {
	tx := &UpdateTransaction{
		SchemaVersion: updateTransactionVersion,
		FromVersion:   fromVersion,
		ToVersion:     toVersion,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		TargetKind:    "app-bundle",
		TargetPath:    filepath.Clean(strings.TrimSpace(appPath)),
		BackupPath:    filepath.Clean(strings.TrimSpace(backupPath)),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if !strings.HasSuffix(strings.ToLower(tx.TargetPath), ".app") ||
		tx.BackupPath != tx.TargetPath+".deepzora-update-backup" {
		return nil, fmt.Errorf("prepare update: invalid macOS bundle paths")
	}
	return tx, nil
}

// ClaimPendingAppBundleUpdateHandoff authorizes a detached child to perform the
// recorded bundle swap. It returns with both the pending transaction lock and
// the target mutation locks held; release must be called on every path.
func ClaimPendingAppBundleUpdateHandoff(expectedToVersion, expectedCreatedAt string, timeout time.Duration) (*UpdateTransaction, func(), error) {
	tx, err := ReadPendingUpdate()
	if err != nil {
		return nil, nil, fmt.Errorf("claim update handoff: read pending transaction: %w", err)
	}
	if tx.TargetKind != "app-bundle" ||
		strings.TrimSpace(tx.ToVersion) != strings.TrimSpace(expectedToVersion) ||
		strings.TrimSpace(tx.CreatedAt) != strings.TrimSpace(expectedCreatedAt) {
		return nil, nil, fmt.Errorf("claim update handoff: pending transaction does not match")
	}
	return claimPendingAppBundleUpdateHandoff(
		expectedToVersion,
		expectedCreatedAt,
		UpdateTransactionID(tx),
		timeout,
	)
}

// ClaimPendingAppBundleUpdateHandoffExact additionally binds the detached
// updater to the complete transaction prepared by the parent process.
func ClaimPendingAppBundleUpdateHandoffExact(
	expectedToVersion, expectedCreatedAt, expectedTransactionID string,
	timeout time.Duration,
) (*UpdateTransaction, func(), error) {
	expectedTransactionID = strings.TrimSpace(expectedTransactionID)
	if expectedTransactionID == "" {
		return nil, nil, fmt.Errorf("claim update handoff: transaction identity is incomplete")
	}
	return claimPendingAppBundleUpdateHandoff(
		expectedToVersion,
		expectedCreatedAt,
		expectedTransactionID,
		timeout,
	)
}

func claimPendingAppBundleUpdateHandoff(
	expectedToVersion, expectedCreatedAt, expectedTransactionID string,
	timeout time.Duration,
) (*UpdateTransaction, func(), error) {
	expectedToVersion = strings.TrimSpace(expectedToVersion)
	expectedCreatedAt = strings.TrimSpace(expectedCreatedAt)
	if expectedToVersion == "" || expectedCreatedAt == "" {
		return nil, nil, fmt.Errorf("claim update handoff: transaction identity is incomplete")
	}
	unlockPending, err := acquirePendingUpdateLock()
	if err != nil {
		return nil, nil, fmt.Errorf("claim update handoff: lock pending transaction: %w", err)
	}
	fail := func(err error) (*UpdateTransaction, func(), error) {
		unlockPending()
		return nil, nil, err
	}

	tx, err := ReadPendingUpdate()
	if err != nil {
		return fail(fmt.Errorf("claim update handoff: read pending transaction: %w", err))
	}
	if tx.TargetKind != "app-bundle" ||
		strings.TrimSpace(tx.ToVersion) != expectedToVersion ||
		strings.TrimSpace(tx.CreatedAt) != expectedCreatedAt {
		return fail(fmt.Errorf("claim update handoff: pending transaction does not match"))
	}
	if expectedTransactionID != "" && UpdateTransactionID(tx) != expectedTransactionID {
		return fail(fmt.Errorf("claim update handoff: pending transaction changed"))
	}
	if tx.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return fail(fmt.Errorf("claim update handoff: pending transaction platform does not match"))
	}
	if err := validateAppBundleHandoffMetadata(tx); err != nil {
		return fail(fmt.Errorf("claim update handoff: %w", err))
	}
	if strings.TrimSpace(tx.HandoffAppPath) == "" ||
		strings.TrimSpace(tx.HandoffStagingPath) == "" ||
		tx.HandoffOwnerPID <= 0 {
		return fail(fmt.Errorf("claim update handoff: handoff metadata is missing"))
	}

	unlockTargets, err := lockRepairMutationsTimeout(timeout, pendingUpdateTargetPaths(tx)...)
	if err != nil {
		return fail(fmt.Errorf("claim update handoff: lock targets: %w", err))
	}
	current, err := ReadPendingUpdate()
	if err != nil {
		unlockTargets()
		return fail(fmt.Errorf("claim update handoff: re-read pending transaction: %w", err))
	}
	if !reflect.DeepEqual(tx, current) {
		unlockTargets()
		return fail(fmt.Errorf("claim update handoff: pending transaction changed while waiting"))
	}
	if err := verifyAppBundleUpdateHandoffBackupAbsent(current); err != nil {
		unlockTargets()
		return fail(fmt.Errorf("claim update handoff: %w", err))
	}
	if err := VerifyAppBundleUpdateHandoffSource(current); err != nil {
		unlockTargets()
		return fail(fmt.Errorf("claim update handoff: %w", err))
	}
	if err := VerifyAppBundleUpdateHandoffOriginal(current); err != nil {
		unlockTargets()
		return fail(fmt.Errorf("claim update handoff: %w", err))
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			unlockTargets()
			unlockPending()
		})
	}
	return current, release, nil
}

// CancelPendingAppBundleUpdateHandoff abandons an exact handoff only when the
// original installed bundle is still the tree captured during prepare. This is
// the safe recovery path when source verification fails after the desktop has
// exited but before any bundle swap occurred.
func CancelPendingAppBundleUpdateHandoff(
	expectedToVersion, expectedCreatedAt string,
	timeout time.Duration,
) (*UpdateTransaction, error) {
	tx, err := ReadPendingUpdate()
	if err != nil {
		return nil, fmt.Errorf("cancel update handoff: read pending transaction: %w", err)
	}
	if tx.TargetKind != "app-bundle" ||
		strings.TrimSpace(tx.ToVersion) != strings.TrimSpace(expectedToVersion) ||
		strings.TrimSpace(tx.CreatedAt) != strings.TrimSpace(expectedCreatedAt) {
		return nil, fmt.Errorf("cancel update handoff: pending transaction does not match")
	}
	return cancelPendingAppBundleUpdateHandoff(
		expectedToVersion,
		expectedCreatedAt,
		timeout,
		UpdateTransactionID(tx),
	)
}

// CancelPendingAppBundleUpdateHandoffExact abandons only the full transaction
// read or prepared by the caller. It is safe to use after a PID wait or failed
// claim where pending state may have been rewritten with copied scalar IDs.
func CancelPendingAppBundleUpdateHandoffExact(
	expected *UpdateTransaction,
	timeout time.Duration,
) (*UpdateTransaction, error) {
	if expected == nil {
		return nil, fmt.Errorf("cancel update handoff: transaction identity is incomplete")
	}
	return cancelPendingAppBundleUpdateHandoff(
		expected.ToVersion,
		expected.CreatedAt,
		timeout,
		repairPlanStateID(expected),
	)
}

func cancelPendingAppBundleUpdateHandoff(
	expectedToVersion, expectedCreatedAt string,
	timeout time.Duration,
	expectedTransactionID string,
) (*UpdateTransaction, error) {
	expectedToVersion = strings.TrimSpace(expectedToVersion)
	expectedCreatedAt = strings.TrimSpace(expectedCreatedAt)
	if expectedToVersion == "" || expectedCreatedAt == "" {
		return nil, fmt.Errorf("cancel update handoff: transaction identity is incomplete")
	}
	unlockPending, err := acquirePendingUpdateLock()
	if err != nil {
		return nil, fmt.Errorf("cancel update handoff: lock pending transaction: %w", err)
	}
	defer unlockPending()
	tx, err := ReadPendingUpdate()
	if err != nil {
		return nil, fmt.Errorf("cancel update handoff: read pending transaction: %w", err)
	}
	if tx.TargetKind != "app-bundle" ||
		strings.TrimSpace(tx.ToVersion) != expectedToVersion ||
		strings.TrimSpace(tx.CreatedAt) != expectedCreatedAt {
		return nil, fmt.Errorf("cancel update handoff: pending transaction does not match")
	}
	if expected := strings.TrimSpace(expectedTransactionID); expected != "" && expected != repairPlanStateID(tx) {
		return nil, fmt.Errorf("cancel update handoff: pending transaction changed")
	}
	unlockTargets, err := lockRepairMutationsTimeout(timeout, pendingUpdateTargetPaths(tx)...)
	if err != nil {
		return nil, fmt.Errorf("cancel update handoff: lock targets: %w", err)
	}
	defer unlockTargets()
	current, err := ReadPendingUpdate()
	if err != nil {
		return nil, fmt.Errorf("cancel update handoff: re-read pending transaction: %w", err)
	}
	if !reflect.DeepEqual(tx, current) {
		return nil, fmt.Errorf("cancel update handoff: pending transaction changed while waiting")
	}
	if err := VerifyAppBundleUpdateHandoffOriginal(current); err != nil {
		return nil, fmt.Errorf("cancel update handoff: %w", err)
	}
	if err := verifyAppBundleUpdateHandoffBackupAbsent(current); err != nil {
		return nil, fmt.Errorf("cancel update handoff: %w", err)
	}
	if err := removePendingUpdateExactVerified(current, func() error {
		if err := VerifyAppBundleUpdateHandoffOriginal(current); err != nil {
			return fmt.Errorf("cancel update handoff: %w", err)
		}
		if err := verifyAppBundleUpdateHandoffBackupAbsent(current); err != nil {
			return fmt.Errorf("cancel update handoff: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return current, nil
}

func sameRepairMutationPaths(a, b []string) bool {
	keys := func(paths []string) []string {
		seen := make(map[string]struct{}, len(paths))
		result := make([]string, 0, len(paths))
		for _, path := range paths {
			key := canonicalRepairPath(path)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, key)
		}
		sort.Strings(result)
		return result
	}
	return reflect.DeepEqual(keys(a), keys(b))
}

// VerifyAppBundleUpdateHandoffSource checks the real staging containment and
// the complete staged tree immediately before a handoff mutates the install.
// The lexical metadata check remains readable after staging cleanup, while this
// stronger check is only used while the source bundle still exists.
func VerifyAppBundleUpdateHandoffSource(tx *UpdateTransaction) error {
	if tx == nil || tx.TargetKind != "app-bundle" {
		return fmt.Errorf("handoff source transaction is invalid")
	}
	if strings.TrimSpace(tx.HandoffAppTreeID) == "" {
		return fmt.Errorf("handoff source digest is missing")
	}
	if strings.TrimSpace(tx.HandoffStagingTreeID) == "" {
		return fmt.Errorf("handoff staging digest is missing")
	}
	if err := validateAppBundleHandoffSourcePaths(tx); err != nil {
		return err
	}
	matched, err := repairPlanTreeHandoffAppMatches(tx.HandoffAppPath, tx.HandoffAppTreeID)
	if err != nil {
		return fmt.Errorf("read staged bundle digest: %w", err)
	}
	if !matched {
		return fmt.Errorf("staged bundle changed after verification")
	}
	actual, err := repairPlanTreeContentStateID(tx.HandoffStagingPath)
	if err != nil {
		return fmt.Errorf("read staging directory digest: %w", err)
	}
	if actual != tx.HandoffStagingTreeID {
		return fmt.Errorf("staging directory changed after verification")
	}
	return nil
}

// CleanupAppBundleUpdateHandoffStaging removes only the complete staging tree
// recorded by the transaction. The root is first displaced to a unique sibling,
// so a concurrent recreation at the public staging path survives.
func CleanupAppBundleUpdateHandoffStaging(tx *UpdateTransaction) error {
	if tx == nil || strings.TrimSpace(tx.HandoffStagingTreeID) == "" {
		return fmt.Errorf("cleanup update staging: transaction identity is incomplete")
	}
	if err := validateAppBundleHandoffMetadata(tx); err != nil {
		return fmt.Errorf("cleanup update staging: %w", err)
	}
	relApp, err := filepath.Rel(tx.HandoffStagingPath, tx.HandoffAppPath)
	if err != nil || relApp == "." || relApp == ".." || strings.HasPrefix(relApp, ".."+string(filepath.Separator)) {
		return fmt.Errorf("cleanup update staging: app path is invalid")
	}
	return removeUpdateNodeMatching(tx.HandoffStagingPath, func(moved string) error {
		actual, err := repairPlanTreeContentStateID(moved)
		if err != nil {
			return err
		}
		if actual != tx.HandoffStagingTreeID {
			return fmt.Errorf("staging directory changed before cleanup")
		}
		return verifyAppBundleUpdateHandoffReplacement(
			tx,
			filepath.Join(moved, relApp),
			"staged",
		)
	}, true)
}

// CleanupAppBundleUpdateReplacement removes a displaced replacement only when
// its complete tree still matches the transaction's verified source.
func CleanupAppBundleUpdateReplacement(tx *UpdateTransaction, path string) error {
	return removeUpdateNodeMatching(path, func(moved string) error {
		return verifyAppBundleUpdateHandoffReplacement(tx, moved, "replacement")
	}, true)
}

// VerifyAppBundleUpdateHandoffTarget proves that the bytes copied into the
// installed bundle are the same tree that was verified in staging.
func VerifyAppBundleUpdateHandoffTarget(tx *UpdateTransaction) error {
	if tx == nil {
		return fmt.Errorf("handoff target transaction is invalid")
	}
	return verifyAppBundleUpdateHandoffReplacement(tx, tx.TargetPath, "installed")
}

// VerifyAppBundleUpdateHandoffReplacement proves that a candidate replacement
// tree matches the bundle captured during prepare. The macOS handoff uses this
// before atomically publishing a sibling staging bundle at the install path.
func VerifyAppBundleUpdateHandoffReplacement(tx *UpdateTransaction, path string) error {
	return verifyAppBundleUpdateHandoffReplacement(tx, path, "replacement")
}

func verifyAppBundleUpdateHandoffReplacement(tx *UpdateTransaction, path, subject string) error {
	if tx == nil || tx.TargetKind != "app-bundle" {
		return fmt.Errorf("handoff target transaction is invalid")
	}
	if strings.TrimSpace(tx.HandoffAppTreeID) == "" {
		return fmt.Errorf("handoff target digest is missing")
	}
	matched, err := repairPlanTreeHandoffAppMatches(path, tx.HandoffAppTreeID)
	if err != nil {
		return fmt.Errorf("read %s bundle digest: %w", subject, err)
	}
	if !matched {
		return fmt.Errorf("%s bundle differs from verified staging", subject)
	}
	return nil
}

// VerifyAppBundleUpdateHandoffOriginal checks that the installed bundle about
// to become the rollback backup is still the tree captured during prepare.
func VerifyAppBundleUpdateHandoffOriginal(tx *UpdateTransaction) error {
	if tx == nil || tx.TargetKind != "app-bundle" {
		return fmt.Errorf("handoff original transaction is invalid")
	}
	return verifyAppBundleUpdateTree(tx.TargetPath, tx.BackupTreeID, "installed bundle changed after prepare")
}

// VerifyAppBundleUpdateHandoffBackup checks the node produced by the
// target-to-backup rename before the replacement bundle is copied into place.
func VerifyAppBundleUpdateHandoffBackup(tx *UpdateTransaction) error {
	if tx == nil || tx.TargetKind != "app-bundle" {
		return fmt.Errorf("handoff backup transaction is invalid")
	}
	return verifyAppBundleUpdateTree(tx.BackupPath, tx.BackupTreeID, "rollback backup differs from prepared bundle")
}

func verifyAppBundleUpdateHandoffBackupAbsent(tx *UpdateTransaction) error {
	if tx == nil || tx.TargetKind != "app-bundle" || strings.TrimSpace(tx.BackupPath) == "" {
		return fmt.Errorf("handoff backup transaction is invalid")
	}
	if _, err := os.Lstat(tx.BackupPath); err == nil {
		return fmt.Errorf("handoff backup path already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect handoff backup path: %w", err)
	}
	return nil
}

func verifyAppBundleUpdateTree(path, expected, mismatch string) error {
	if strings.TrimSpace(expected) == "" {
		return fmt.Errorf("original bundle digest is missing")
	}
	actual, err := repairPlanTreeContentStateID(path)
	if err != nil {
		return fmt.Errorf("read original bundle digest: %w", err)
	}
	if actual != expected {
		return fmt.Errorf("%s", mismatch)
	}
	return nil
}

// AppBundleTreeDigest exposes the deterministic bundle-content digest to the
// desktop handoff tests and other platform glue without exposing path identity.
func AppBundleTreeDigest(path string) (string, error) {
	return repairPlanTreeContentStateID(path)
}

func validateAppBundleHandoffSourcePaths(tx *UpdateTransaction) error {
	staging, err := filepath.EvalSymlinks(tx.HandoffStagingPath)
	if err != nil {
		return fmt.Errorf("resolve handoff staging directory: %w", err)
	}
	app, err := filepath.EvalSymlinks(tx.HandoffAppPath)
	if err != nil {
		return fmt.Errorf("resolve handoff app bundle: %w", err)
	}
	tempRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve temporary directory: %w", err)
	}
	within := func(root, path string) bool {
		rel, relErr := filepath.Rel(root, path)
		return relErr == nil && rel != "." && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !within(tempRoot, staging) {
		return fmt.Errorf("handoff staging directory resolves outside the system temporary directory")
	}
	if !within(staging, app) {
		return fmt.Errorf("handoff app bundle resolves outside its staging directory")
	}
	if info, statErr := os.Stat(app); statErr != nil || !info.IsDir() {
		if statErr != nil {
			return fmt.Errorf("handoff app bundle is unavailable: %w", statErr)
		}
		return fmt.Errorf("handoff app bundle is not a directory")
	}
	return nil
}

// ClearClaimedAppBundleUpdateHandoff removes a failed handoff transaction.
// The caller must still hold the claim returned above.
func ClearClaimedAppBundleUpdateHandoff(claimed *UpdateTransaction) error {
	current, err := ReadPendingUpdate()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(claimed, current) {
		return fmt.Errorf("clear update handoff: pending transaction changed")
	}
	if err := VerifyAppBundleUpdateHandoffOriginal(current); err != nil {
		return fmt.Errorf("clear update handoff: %w", err)
	}
	if err := verifyAppBundleUpdateHandoffBackupAbsent(current); err != nil {
		return fmt.Errorf("clear update handoff: %w", err)
	}
	if err := removePendingUpdateExactVerified(current, func() error {
		if err := VerifyAppBundleUpdateHandoffOriginal(current); err != nil {
			return fmt.Errorf("clear update handoff: %w", err)
		}
		if err := verifyAppBundleUpdateHandoffBackupAbsent(current); err != nil {
			return fmt.Errorf("clear update handoff: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// WritePendingUpdate is retained for source compatibility with older repair
// callers. Pending transactions are immutable once created; callers that need
// to start an update should use the prepare APIs, and callers that need to
// transition one must use the exact transaction helpers below.
//
// Deprecated: this function only creates a pending transaction and refuses to
// replace an existing one.
func WritePendingUpdate(tx *UpdateTransaction) error {
	return createPendingUpdate(tx)
}

func createPendingUpdate(tx *UpdateTransaction) error {
	return writePendingUpdate(tx, true)
}

func writePendingUpdate(tx *UpdateTransaction, createOnly bool) error {
	if tx == nil {
		return fmt.Errorf("pending update: nil transaction")
	}
	path := PendingUpdatePath()
	if path == "" {
		return fmt.Errorf("pending update: Deepzora state directory is unavailable")
	}
	b, err := json.MarshalIndent(tx, "", "  ")
	if err != nil {
		return err
	}
	if createOnly {
		return fileutil.AtomicCreateFile(path, append(b, '\n'), 0o600)
	}
	return fileutil.AtomicWriteFile(path, append(b, '\n'), 0o600)
}

func removePendingUpdateExactVerified(expected *UpdateTransaction, verify func() error) error {
	if expected == nil {
		return fmt.Errorf("clear pending update: transaction identity is incomplete")
	}
	path := PendingUpdatePath()
	pendingUpdateBeforeCleanup(path)
	cleanup, err := moveRepairNodeToUniqueCleanup(path)
	if err != nil {
		return err
	}
	if cleanup == "" {
		return fmt.Errorf("clear pending update: pending transaction disappeared before commit")
	}
	updateCleanupAfterRename(path, cleanup)
	restore := func(cause error) error {
		if restoreErr := renameRepairNodeNoReplace(cleanup, path); restoreErr != nil {
			return fmt.Errorf("%w; pending transaction retained at %s: %w", cause, cleanup, restoreErr)
		}
		return cause
	}
	b, err := os.ReadFile(cleanup)
	if err != nil {
		return restore(err)
	}
	var actual UpdateTransaction
	if err := json.Unmarshal(b, &actual); err != nil {
		return restore(err)
	}
	if UpdateTransactionID(&actual) != UpdateTransactionID(expected) {
		return restore(fmt.Errorf("clear pending update: pending transaction changed"))
	}
	if verify != nil {
		if err := verify(); err != nil {
			return restore(err)
		}
	}
	if err := removePendingUpdateFile(cleanup); err != nil {
		return restore(err)
	}
	return nil
}

// ensureNoPendingUpdate runs with the pending-update lock held. Preparing a new
// transaction over an existing one would overwrite fixed backup paths before
// the new transaction is durable, destroying the previous rollback material if
// preparation later fails.
func ensureNoPendingUpdate() error {
	disposition, tx, err := classifyPendingUpdate()
	if err != nil {
		return fmt.Errorf("prepare update: %w", err)
	}
	switch disposition {
	case pendingUpdateActionable:
		if tx == nil {
			// Self-describing but not valid for this installation (see
			// ReconcilePendingUpdate): nothing can resume or roll it back, and
			// refusing here would refuse forever. Quarantine the marker so a
			// future update can proceed; target and rollback material are
			// untouched.
			if _, err := quarantinePendingUpdate("not valid for this installation"); err != nil {
				return fmt.Errorf("prepare update: quarantine unusable transaction: %w", err)
			}
			return nil
		}
		return fmt.Errorf("prepare update: a pending update already exists")
	case pendingUpdateDebris:
		// Refusing here would be refusing forever: debris cannot be resumed,
		// rolled back, or cleared by reconciliation, so every future update
		// would fail on a transaction nothing can act on.
		if _, err := quarantinePendingUpdate("blocked a new update"); err != nil {
			return fmt.Errorf("prepare update: quarantine unusable transaction: %w", err)
		}
	}
	return nil
}

// pendingUpdateDisposition is what the pending-update marker on disk currently
// means. Preparation and reconciliation both classify through it so they cannot
// disagree about whether a transaction exists — when they did, a marker that
// reconciliation could not act on still made preparation refuse, and updates
// stayed blocked permanently (#7342).
type pendingUpdateDisposition int

const (
	// pendingUpdateNone: no marker on disk.
	pendingUpdateNone pendingUpdateDisposition = iota
	// pendingUpdateActionable: a transaction that can still be resumed or
	// rolled back. Preparation must refuse over one of these — writing a new
	// transaction would overwrite fixed backup paths and destroy the rollback
	// material this one still owns.
	pendingUpdateActionable
	// pendingUpdateDebris: a marker that cannot describe a recoverable
	// transaction, so it owns no rollback material worth protecting.
	pendingUpdateDebris
)

// classifyPendingUpdate reads the marker and decides what can be done with it.
//
// The line between debris and an actionable transaction is deliberately drawn
// at self-description. A transaction that cannot be parsed, or that does not
// say which release it targets, for which platform, and when it was opened,
// names nothing to roll back to — discarding it loses nothing. Every other
// validation failure is environment-relative (the launcher path, whether the
// target sits inside this desktop installation, where the backup lives) and can
// fail for a perfectly good transaction observed from the wrong install, so
// those keep the old refusal rather than risking real rollback material.
//
// IO failures are errors, never debris: an unreadable marker is not an absent
// one, and quarantining on a transient permission error would throw away a
// recoverable transaction.
func classifyPendingUpdate() (pendingUpdateDisposition, *UpdateTransaction, error) {
	path := PendingUpdatePath()
	if path == "" {
		return pendingUpdateNone, nil, fmt.Errorf("Deepzora state directory is unavailable")
	}
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return pendingUpdateNone, nil, nil
		}
		return pendingUpdateNone, nil, fmt.Errorf("inspect pending transaction: %w", err)
	}
	tx, err := readPendingUpdateUnchecked()
	if err != nil {
		switch {
		case os.IsNotExist(err):
			return pendingUpdateNone, nil, nil
		case isPendingUpdateContentError(err):
			return pendingUpdateDebris, nil, nil
		default:
			return pendingUpdateNone, nil, fmt.Errorf("read pending transaction: %w", err)
		}
	}
	if !pendingUpdateSelfDescribing(tx) {
		return pendingUpdateDebris, nil, nil
	}
	if err := validateUpdateTransaction(tx); err != nil {
		if errors.Is(err, errPendingUpdateForeignInstall) {
			return pendingUpdateActionable, nil, nil
		}
		return pendingUpdateNone, nil, fmt.Errorf("validate pending transaction: %w", err)
	}
	return pendingUpdateActionable, tx, nil
}

// isPendingUpdateContentError reports whether err means the bytes on disk are
// not a transaction, as opposed to the file being unreadable. A prepare
// interrupted mid-write leaves a truncated object, which decodes to a syntax
// error rather than an IO error.
func isPendingUpdateContentError(err error) bool {
	var syntax *json.SyntaxError
	var unmarshalType *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &unmarshalType) || errors.Is(err, io.ErrUnexpectedEOF)
}

// pendingUpdateSelfDescribing reports whether tx carries the identity any
// recovery needs regardless of where Deepzora is installed: which release it
// targets, for which platform, and when it was opened.
func pendingUpdateSelfDescribing(tx *UpdateTransaction) bool {
	if tx == nil || tx.SchemaVersion != updateTransactionVersion || strings.TrimSpace(tx.ToVersion) == "" {
		return false
	}
	if strings.TrimSpace(tx.Platform) == "" || strings.TrimSpace(tx.CreatedAt) == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(tx.CreatedAt))
	return err == nil
}

// quarantinePendingUpdate moves an unusable marker aside and returns where it
// went. It is deliberately not a delete: the marker is the only evidence of
// what went wrong, and a user who reports a stuck updater should still have it.
// Callers hold the pending-update lock.
func quarantinePendingUpdate(reason string) (string, error) {
	path := PendingUpdatePath()
	if path == "" {
		return "", fmt.Errorf("Deepzora state directory is unavailable")
	}
	base := path + ".unusable-" + time.Now().UTC().Format("20060102T150405Z")
	aside := base
	for i := 1; ; i++ {
		if _, err := os.Lstat(aside); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", err
		}
		aside = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.Rename(path, aside); err != nil {
		return "", err
	}
	slog.Warn("repair: quarantined an unusable pending update transaction",
		"path", aside, "reason", reason)
	return aside, nil
}

func pendingUpdateMarkerDigest(path string) (string, error) {
	body, err := readRepairRegularFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// quarantinePendingUpdateAfterReconcile rechecks an unusable marker while
// holding the cross-process pending lock. Reconciliation initially classifies
// without that lock because the normal cancel/rollback paths acquire it later;
// the marker digest prevents a concurrent prepare from being quarantined after
// it has replaced the marker.
func quarantinePendingUpdateAfterReconcile(reason string, expectedDigest string, wantForeign bool) (bool, error) {
	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return false, fmt.Errorf("lock pending transaction: %w", err)
	}
	defer unlock()

	path := PendingUpdatePath()
	actualDigest, err := pendingUpdateMarkerDigest(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read pending transaction: %w", err)
	}
	if actualDigest != expectedDigest {
		return false, fmt.Errorf("pending update changed while waiting")
	}
	disposition, tx, err := classifyPendingUpdate()
	if err != nil {
		return false, err
	}
	if wantForeign {
		if disposition != pendingUpdateActionable || tx != nil {
			return false, fmt.Errorf("pending update is no longer a foreign transaction")
		}
	} else if disposition != pendingUpdateDebris {
		return false, fmt.Errorf("pending update is no longer unusable debris")
	}
	if _, err := quarantinePendingUpdate(reason); err != nil {
		return false, err
	}
	return true, nil
}

func ReadPendingUpdate() (*UpdateTransaction, error) {
	tx, err := readPendingUpdateUnchecked()
	if err != nil {
		return nil, err
	}
	if err := validateUpdateTransaction(tx); err != nil {
		return nil, err
	}
	return tx, nil
}

func readPendingUpdateForLauncher(launcherPath string) (*UpdateTransaction, error) {
	tx, err := readPendingUpdateUnchecked()
	if err != nil {
		return nil, err
	}
	if err := validateUpdateTransactionForLauncher(tx, launcherPath); err != nil {
		return nil, err
	}
	return tx, nil
}

func readPendingUpdateUnchecked() (*UpdateTransaction, error) {
	path := PendingUpdatePath()
	if path == "" {
		return nil, os.ErrNotExist
	}
	b, err := readRepairRegularFile(path)
	if err != nil {
		return nil, err
	}
	var tx UpdateTransaction
	if err := json.Unmarshal(b, &tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

func HasPendingUpdate() bool {
	_, err := ReadPendingUpdate()
	return err == nil
}

// PendingUpdateExists reports the on-disk marker even when its contents are
// malformed. It is intended only for progress/UI decisions; callers must use
// ReadPendingUpdate or ReconcilePendingUpdate before authorizing mutations.
func PendingUpdateExists() bool {
	path := PendingUpdatePath()
	if path == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

// ReconcilePendingUpdate resolves an older immutable update transaction before
// startup or a new install. It first attempts the narrow cancellation path,
// which succeeds only while every original target still matches the state
// captured by prepare and no replacement state is durable. If publication has
// started, it falls back to the exact verified rollback path. Both transitions
// re-read the complete transaction under the pending and target mutation locks.
//
// A transaction targeting runningVersion is left untouched only when the
// transaction also proves that its replacement release unit is installed and
// its rollback state is intact. Version equality alone is not installation
// evidence: a same-version/manual launch may observe an abandoned prepare.
func ReconcilePendingUpdate(runningVersion string) (PendingUpdateReconcileResult, error) {
	disposition, tx, classifyErr := classifyPendingUpdate()
	if classifyErr != nil {
		return PendingUpdateReconcileResult{Pending: true}, fmt.Errorf("reconcile pending update: %w", classifyErr)
	}
	switch {
	case disposition == pendingUpdateNone:
		return PendingUpdateReconcileResult{}, nil
	case disposition == pendingUpdateDebris:
		// Nothing here can be resumed or rolled back. Leaving it in place is
		// what stranded users: startup kept failing to recover it while
		// preparation kept refusing to write over it.
		digest, digestErr := pendingUpdateMarkerDigest(PendingUpdatePath())
		if digestErr != nil {
			if os.IsNotExist(digestErr) {
				return PendingUpdateReconcileResult{}, nil
			}
			return PendingUpdateReconcileResult{Pending: true}, fmt.Errorf("reconcile pending update: read marker: %w", digestErr)
		}
		quarantined, err := quarantinePendingUpdateAfterReconcile("no recoverable transaction to reconcile", digest, false)
		if err != nil {
			return PendingUpdateReconcileResult{Pending: true}, fmt.Errorf("reconcile pending update: quarantine unusable transaction: %w", err)
		}
		if !quarantined {
			return PendingUpdateReconcileResult{}, nil
		}
		return PendingUpdateReconcileResult{Pending: true, Cleared: true}, nil
	case tx == nil:
		// Self-describing but not valid for this installation: the launcher and
		// target directories no longer match (install layout moved to
		// versions\<version>\ or the old install directory is gone). Nothing
		// here can be resumed or rolled back by this installation, and leaving
		// the marker blocks every future update permanently — recovery fails
		// here before preparation can act, and the marker lives in the state
		// directory so a reinstall does not clear it (#7391, #7416, #7407).
		// Quarantine the marker, never a delete: the target and rollback
		// material are untouched, so a genuine transaction observed from the
		// wrong install loses nothing and remains recoverable from the
		// .unusable-* file by hand.
		digest, digestErr := pendingUpdateMarkerDigest(PendingUpdatePath())
		if digestErr != nil {
			if os.IsNotExist(digestErr) {
				return PendingUpdateReconcileResult{}, nil
			}
			return PendingUpdateReconcileResult{Pending: true}, fmt.Errorf("reconcile pending update: read marker: %w", digestErr)
		}
		quarantined, err := quarantinePendingUpdateAfterReconcile("not valid for this installation", digest, true)
		if err != nil {
			return PendingUpdateReconcileResult{Pending: true}, fmt.Errorf("reconcile pending update: quarantine unusable transaction: %w", err)
		}
		if !quarantined {
			return PendingUpdateReconcileResult{}, nil
		}
		return PendingUpdateReconcileResult{Pending: true, Cleared: true}, nil
	}
	result := PendingUpdateReconcileResult{
		Pending:     true,
		FromVersion: tx.FromVersion,
		ToVersion:   tx.ToVersion,
		TargetPath:  tx.TargetPath,
	}
	if UpdateVersionsEqual(runningVersion, tx.ToVersion) &&
		pendingUpdateInstalledForHealth(tx) {
		// After the stale window, auto-commit a still-running probationary target.
		if pendingUpdateHealthIsStale(tx) {
			if healErr := MarkUpdateHealthy(runningVersion); healErr == nil && !PendingUpdateExists() {
				result.Pending = false
				result.Healthy = true
				result.Cleared = true
				return result, nil
			} else if healErr != nil {
				slog.Warn("repair: stale probationary update could not be committed automatically",
					"toVersion", tx.ToVersion, "err", healErr)
			}
		}
		result.AwaitingHealth = true
		return result, ErrPendingUpdateAwaitingHealth
	}

	// Cancel is deliberately attempted before rollback. For app bundles it
	// requires the original tree and an absent backup; for file release units it
	// requires every prepared target and no installed-state sidecar. A failed
	// cancel never mutates the transaction or release unit.
	if cancelErr := CancelPendingUpdateExact(tx); cancelErr == nil {
		// Exact cancellation historically treats a different target version or
		// creation time as an inert success. Re-check the public postcondition so
		// reconciliation never reports a newer transaction as cleared.
		current, currentErr := ReadPendingUpdate()
		if os.IsNotExist(currentErr) {
			result.Cleared = true
			cleanupPendingUpdateStaging(tx)
			return result, nil
		}
		if currentErr != nil {
			return result, fmt.Errorf("reconcile pending update: verify cancellation: %w", currentErr)
		}
		if !reflect.DeepEqual(tx, current) {
			return result, fmt.Errorf("reconcile pending update: pending transaction changed during cancellation")
		}
		return result, fmt.Errorf("reconcile pending update: transaction remained after cancellation")
	}

	rollback, rollbackErr := RollbackPendingUpdateExact(tx)
	if rollbackErr != nil {
		result.RolledBack = rollback.RolledBack
		result.MixedInstall = rollback.MixedInstall
		return result, fmt.Errorf("reconcile pending update: %w", rollbackErr)
	}
	if !rollback.RolledBack {
		// Another exact owner may have committed or cancelled the transaction
		// between the invocation snapshot and the locked transition.
		if _, currentErr := ReadPendingUpdate(); os.IsNotExist(currentErr) {
			return PendingUpdateReconcileResult{}, nil
		}
		return result, fmt.Errorf("reconcile pending update: transaction could not be cancelled or rolled back")
	}
	result.RolledBack = true
	cleanupPendingUpdateStaging(tx)
	return result, nil
}

// CommitProbationaryPendingUpdate commits a still-running probationary update
// when install evidence matches. Returns true when the marker is gone.
func CommitProbationaryPendingUpdate(runningVersion string) (bool, error) {
	if strings.TrimSpace(runningVersion) == "" {
		return false, nil
	}
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	if !UpdateVersionsEqual(runningVersion, tx.ToVersion) || !pendingUpdateInstalledForHealth(tx) {
		return false, nil
	}
	if err := MarkUpdateHealthy(runningVersion); err != nil {
		return false, err
	}
	return !PendingUpdateExists(), nil
}

// AbandonPendingUpdate is the user-initiated recovery path for a stuck
// transaction: commit if possible, else reconcile, else force-retire.
func AbandonPendingUpdate(runningVersion string) (PendingUpdateReconcileResult, error) {
	committed, commitErr := CommitProbationaryPendingUpdate(runningVersion)
	if commitErr == nil && committed {
		return PendingUpdateReconcileResult{Cleared: true, Healthy: true}, nil
	}
	if commitErr != nil {
		// Keep going: a drifted backup must not block explicit discard.
		slog.Debug("repair: probationary commit during abandon failed; continuing",
			"err", commitErr)
	}
	result, reconcileErr := ReconcilePendingUpdate(runningVersion)
	if reconcileErr == nil {
		return result, nil
	}
	// Force-retire when still AwaitingHealth with the live target installed.
	if errors.Is(reconcileErr, ErrPendingUpdateAwaitingHealth) {
		if retired, retireErr := forceRetireProbationaryPendingUpdate(runningVersion); retireErr != nil {
			return result, fmt.Errorf("abandon pending update: %w", retireErr)
		} else if retired {
			result.Pending = false
			result.AwaitingHealth = false
			result.Healthy = true
			result.Cleared = true
			return result, nil
		}
	}
	if commitErr != nil && reconcileErr != nil {
		return result, fmt.Errorf("abandon pending update: %w", errors.Join(reconcileErr, commitErr))
	}
	return result, reconcileErr
}

// forceRetireProbationaryPendingUpdate retires a probationary marker when the
// live target is installed but MarkUpdateHealthy cannot finish (e.g. bad backup).
func forceRetireProbationaryPendingUpdate(runningVersion string) (bool, error) {
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	// Target-only evidence: broken rollback backups must not block discard.
	if !UpdateVersionsEqual(runningVersion, tx.ToVersion) || !pendingUpdateTargetInstalled(tx) {
		return false, nil
	}
	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return false, fmt.Errorf("force retire probationary update: lock pending transaction: %w", err)
	}
	defer unlock()
	current, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	if UpdateTransactionID(current) != UpdateTransactionID(tx) {
		return false, fmt.Errorf("force retire probationary update: pending transaction changed")
	}
	if !UpdateVersionsEqual(runningVersion, current.ToVersion) || !pendingUpdateTargetInstalled(current) {
		return false, nil
	}
	unlockTargets, lockErr := lockRepairMutations(pendingUpdateTargetPaths(current)...)
	if lockErr != nil {
		return false, fmt.Errorf("force retire probationary update: lock targets: %w", lockErr)
	}
	defer unlockTargets()
	recheck, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	if !reflect.DeepEqual(current, recheck) {
		return false, fmt.Errorf("force retire probationary update: pending transaction changed while waiting")
	}
	verifyInstalled := func() error {
		if !pendingUpdateTargetInstalled(recheck) {
			return fmt.Errorf("installed target no longer matches the pending transaction")
		}
		return nil
	}
	if err := removePendingUpdateExactVerified(recheck, verifyInstalled); err != nil {
		return false, err
	}
	removeUpdateBackups(recheck)
	slog.Warn("repair: force-retired a probationary pending update after explicit abandon",
		"toVersion", recheck.ToVersion, "target", recheck.TargetPath)
	return true, nil
}

// pendingUpdateTargetInstalled reports live replacement install evidence only
// (no rollback backup requirement). Used by force-retire on explicit abandon.
func pendingUpdateTargetInstalled(tx *UpdateTransaction) bool {
	if tx == nil {
		return false
	}
	switch tx.TargetKind {
	case "app-bundle":
		return VerifyAppBundleUpdateHandoffTarget(tx) == nil
	default:
		return false
	}
}

// pendingUpdateInstalledForHealth requires transaction-bound evidence for the
// complete replacement and rollback unit. It intentionally treats missing or
// drifted evidence as uninstalled so reconciliation can take the existing
// exact cancel/rollback paths instead of trusting a version string.
func pendingUpdateInstalledForHealth(tx *UpdateTransaction) bool {
	if tx == nil {
		return false
	}
	switch tx.TargetKind {
	case "app-bundle":
		return VerifyAppBundleUpdateHandoffTarget(tx) == nil &&
			VerifyAppBundleUpdateHandoffBackup(tx) == nil
	default:
		return false
	}
}

// cleanupPendingUpdateStaging is best-effort after the pending transaction has
// been safely committed away. CleanupAppBundleUpdateHandoffStaging verifies the
// complete recorded tree before removal, so drifted or recreated paths survive.
func cleanupPendingUpdateStaging(tx *UpdateTransaction) {
	if tx == nil || tx.TargetKind != "app-bundle" ||
		strings.TrimSpace(tx.HandoffStagingPath) == "" ||
		strings.TrimSpace(tx.HandoffStagingTreeID) == "" {
		return
	}
	_ = CleanupAppBundleUpdateHandoffStaging(tx)
}

func readPendingUpdateInvocation() (*UpdateTransaction, string, map[string]string, error) {
	tx, err := ReadPendingUpdate()
	if err != nil {
		return nil, "", nil, err
	}
	stateID, states := pendingUpdateBoundPreview(tx)
	return tx, stateID, states, nil
}

// MarkUpdateHealthy commits a probationary update and removes its backup. A
// version mismatch is ignored so an older process cannot bless a newer update.
func MarkUpdateHealthy(runningVersion string) error {
	return markUpdateHealthyInvocation(runningVersion, "", "")
}

// MarkUpdateHealthyMatching commits only the exact pending transaction observed
// when this desktop process started. The creation identity prevents an older
// process from blessing a later same-version retry.
func MarkUpdateHealthyMatching(runningVersion, expectedCreatedAt string) error {
	expectedCreatedAt = strings.TrimSpace(expectedCreatedAt)
	if expectedCreatedAt == "" {
		return nil
	}
	return markUpdateHealthyInvocation(runningVersion, expectedCreatedAt, "")
}

// MarkUpdateHealthyExact commits only the complete transaction captured before
// the replacement process started.
func MarkUpdateHealthyExact(runningVersion, expectedCreatedAt, expectedTransactionID string) error {
	expectedCreatedAt = strings.TrimSpace(expectedCreatedAt)
	expectedTransactionID = strings.TrimSpace(expectedTransactionID)
	if expectedCreatedAt == "" || expectedTransactionID == "" {
		return nil
	}
	return markUpdateHealthyInvocation(runningVersion, expectedCreatedAt, expectedTransactionID)
}

func markUpdateHealthyInvocation(runningVersion, expectedCreatedAt, expectedTransactionID string) error {
	tx, stateID, _, err := readPendingUpdateInvocation()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !UpdateVersionsEqual(runningVersion, tx.ToVersion) {
		return nil
	}
	if expected := strings.TrimSpace(expectedCreatedAt); expected != "" && expected != strings.TrimSpace(tx.CreatedAt) {
		return nil
	}
	if expected := strings.TrimSpace(expectedTransactionID); expected != "" && expected != UpdateTransactionID(tx) {
		return fmt.Errorf("mark update healthy: pending transaction changed")
	}
	return markUpdateHealthyMatching(
		runningVersion,
		tx.CreatedAt,
		UpdateTransactionID(tx),
		stateID,
	)
}

func markUpdateHealthyMatching(runningVersion, expectedCreatedAt, expectedTransactionID, expectedStateID string) error {
	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return fmt.Errorf("mark update healthy: lock pending transaction: %w", err)
	}
	defer unlock()
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !UpdateVersionsEqual(runningVersion, tx.ToVersion) {
		return nil
	}
	if expected := strings.TrimSpace(expectedCreatedAt); expected != "" && expected != strings.TrimSpace(tx.CreatedAt) {
		return nil
	}
	if expected := strings.TrimSpace(expectedTransactionID); expected != "" && expected != UpdateTransactionID(tx) {
		return fmt.Errorf("mark update healthy: pending transaction changed")
	}
	unlockTargets, lockErr := lockRepairMutations(pendingUpdateTargetPaths(tx)...)
	if lockErr != nil {
		return fmt.Errorf("mark update healthy: lock targets: %w", lockErr)
	}
	defer unlockTargets()
	current, err := ReadPendingUpdate()
	if err != nil {
		return fmt.Errorf("mark update healthy: re-read pending transaction: %w", err)
	}
	if !reflect.DeepEqual(tx, current) {
		return fmt.Errorf("mark update healthy: pending transaction changed while waiting")
	}
	tx = current
	verifyInvocationState := func() error {
		actual, _ := pendingUpdateBoundPreview(tx)
		if strings.TrimSpace(expectedStateID) != actual {
			return fmt.Errorf("mark update healthy: pending update state changed while waiting")
		}
		return nil
	}
	verifyHealthyState := func() error {
		if err := verifyInvocationState(); err != nil {
			return err
		}
		switch tx.TargetKind {
		case "app-bundle":
			if strings.TrimSpace(tx.HandoffAppTreeID) == "" {
				return fmt.Errorf("mark update healthy: installed bundle state is missing")
			}
			if err := VerifyAppBundleUpdateHandoffTarget(tx); err != nil {
				return fmt.Errorf("mark update healthy: %w", err)
			}
			if strings.TrimSpace(tx.BackupTreeID) == "" {
				return fmt.Errorf("mark update healthy: rollback backup state is missing")
			}
			if err := VerifyAppBundleUpdateHandoffBackup(tx); err != nil {
				return fmt.Errorf("mark update healthy: %w", err)
			}
		}
		return nil
	}
	if err := verifyHealthyState(); err != nil {
		return err
	}
	if err := removePendingUpdateExactVerified(tx, verifyHealthyState); err != nil {
		return err
	}
	removeUpdateBackups(tx)
	return nil
}

// CancelPendingUpdate removes a transaction that failed before control was
// handed to the replacement build. A version mismatch is intentionally inert.
func CancelPendingUpdate(toVersion string) error {
	return cancelPendingUpdateInvocation(toVersion, "", "")
}

// CancelPendingUpdateMatching removes only the exact transaction prepared by
// the caller. It is used by updater failure paths where a same-version retry can
// replace pending-update.json before cleanup runs.
func CancelPendingUpdateMatching(toVersion, expectedCreatedAt string) error {
	expectedCreatedAt = strings.TrimSpace(expectedCreatedAt)
	if expectedCreatedAt == "" {
		return nil
	}
	return cancelPendingUpdateInvocation(toVersion, expectedCreatedAt, "")
}

// CancelPendingUpdateExact removes only the complete transaction returned by
// prepare. This is the updater failure path: copied creation timestamps are not
// sufficient authorization if pending-update.json itself was rewritten.
func CancelPendingUpdateExact(expected *UpdateTransaction) error {
	if expected == nil {
		return fmt.Errorf("cancel pending update: transaction identity is incomplete")
	}
	return cancelPendingUpdateInvocation(
		expected.ToVersion,
		expected.CreatedAt,
		repairPlanStateID(expected),
	)
}

func cancelPendingUpdateInvocation(toVersion, expectedCreatedAt, expectedTransactionID string) error {
	tx, stateID, _, err := readPendingUpdateInvocation()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(toVersion) != strings.TrimSpace(tx.ToVersion) {
		return nil
	}
	if expected := strings.TrimSpace(expectedCreatedAt); expected != "" && expected != strings.TrimSpace(tx.CreatedAt) {
		return nil
	}
	if expected := strings.TrimSpace(expectedTransactionID); expected != "" && expected != UpdateTransactionID(tx) {
		return fmt.Errorf("cancel pending update: pending transaction changed")
	}
	return cancelPendingUpdateMatching(
		tx.ToVersion,
		tx.CreatedAt,
		UpdateTransactionID(tx),
		stateID,
	)
}

func cancelPendingUpdateMatching(toVersion, expectedCreatedAt, expectedTransactionID, expectedStateID string) error {
	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return fmt.Errorf("cancel pending update: lock pending transaction: %w", err)
	}
	defer unlock()
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(toVersion) != strings.TrimSpace(tx.ToVersion) {
		return nil
	}
	if expected := strings.TrimSpace(expectedCreatedAt); expected != "" && expected != strings.TrimSpace(tx.CreatedAt) {
		return nil
	}
	if expected := strings.TrimSpace(expectedTransactionID); expected != "" && expected != repairPlanStateID(tx) {
		return fmt.Errorf("cancel pending update: pending transaction changed")
	}
	unlockTargets, lockErr := lockRepairMutations(pendingUpdateTargetPaths(tx)...)
	if lockErr != nil {
		return fmt.Errorf("cancel pending update: lock targets: %w", lockErr)
	}
	defer unlockTargets()
	current, err := ReadPendingUpdate()
	if err != nil {
		return fmt.Errorf("cancel pending update: re-read pending transaction: %w", err)
	}
	if !reflect.DeepEqual(tx, current) {
		return fmt.Errorf("cancel pending update: pending transaction changed while waiting")
	}
	tx = current
	verifyCancellationState := func() error {
		actual, _ := pendingUpdateBoundPreview(tx)
		if strings.TrimSpace(expectedStateID) != actual {
			return fmt.Errorf("cancel pending update: pending update state changed while waiting")
		}
		switch tx.TargetKind {
		case "app-bundle":
			if err := VerifyAppBundleUpdateHandoffOriginal(tx); err != nil {
				return fmt.Errorf("cancel pending update: %w", err)
			}
			if err := verifyAppBundleUpdateHandoffBackupAbsent(tx); err != nil {
				return fmt.Errorf("cancel pending update: %w", err)
			}
		default:
			return fmt.Errorf("cancel pending update: unsupported target kind %q", tx.TargetKind)
		}
		return nil
	}
	if err := verifyCancellationState(); err != nil {
		return err
	}
	if err := removePendingUpdateExactVerified(tx, verifyCancellationState); err != nil {
		return err
	}

	return nil
}

func removeUpdateBackups(tx *UpdateTransaction) {
	if tx != nil && tx.TargetKind == "app-bundle" {
		_ = removeUpdateBackupTreeMatching(tx.BackupPath, tx.BackupTreeID)
	}
}

func removeUpdateBackupTreeMatching(path, expectedTreeID string) error {
	expectedTreeID = strings.TrimSpace(expectedTreeID)
	if path == "" || expectedTreeID == "" {
		return nil
	}
	return removeUpdateNodeMatching(path, func(moved string) error {
		actual, err := repairPlanTreeContentStateID(moved)
		if err != nil {
			return err
		}
		if actual != expectedTreeID {
			return fmt.Errorf("update backup tree changed")
		}
		return nil
	}, true)
}

func removeUpdateNodeMatching(path string, verify func(string) error, directory bool) error {
	cleanup, err := moveRepairNodeToUniqueCleanup(path)
	if err != nil || cleanup == "" {
		return err
	}
	updateCleanupAfterRename(path, cleanup)
	restore := func(cause error) error {
		if restoreErr := renameRepairNodeNoReplace(cleanup, path); restoreErr != nil {
			return fmt.Errorf("%w; changed update node retained at %s: %w", cause, cleanup, restoreErr)
		}
		return cause
	}
	if err := verify(cleanup); err != nil {
		return restore(err)
	}
	if directory {
		return os.RemoveAll(cleanup)
	}
	if err := os.Remove(cleanup); err != nil {
		return restore(err)
	}
	return nil
}

func RollbackPendingUpdate() (UpdateRollbackResult, error) {
	return rollbackPendingUpdateInvocation("", "", "")
}

// RollbackPendingUpdateMatching rolls back only the exact transaction prepared
// by the caller. This is used when an apply attempt fails after another process
// may already have replaced pending-update.json with a same-version retry.
func RollbackPendingUpdateMatching(expectedToVersion, expectedCreatedAt string) (UpdateRollbackResult, error) {
	expectedToVersion = strings.TrimSpace(expectedToVersion)
	expectedCreatedAt = strings.TrimSpace(expectedCreatedAt)
	if expectedToVersion == "" || expectedCreatedAt == "" {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: transaction identity is incomplete")
	}
	return rollbackPendingUpdateInvocation(expectedToVersion, expectedCreatedAt, "")
}

func rollbackPendingUpdateState(expectedStateID string, expectedStates map[string]string) (UpdateRollbackResult, error) {
	return rollbackPendingUpdateMatching("", "", expectedStateID, expectedStates, "", true)
}

// RollbackPendingUpdateExact restores only the complete transaction returned by
// prepare. It is used after a platform apply failure where a later same-version
// transaction must remain untouched.
func RollbackPendingUpdateExact(expected *UpdateTransaction) (UpdateRollbackResult, error) {
	if expected == nil {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: transaction identity is incomplete")
	}
	return rollbackPendingUpdateInvocation(
		expected.ToVersion,
		expected.CreatedAt,
		repairPlanStateID(expected),
	)
}

func rollbackPendingUpdateInvocation(
	expectedToVersion, expectedCreatedAt, expectedTransactionID string,
) (UpdateRollbackResult, error) {
	tx, stateID, states, err := readPendingUpdateInvocation()
	if err != nil {
		if os.IsNotExist(err) {
			return UpdateRollbackResult{}, nil
		}
		return UpdateRollbackResult{}, err
	}
	if expected := strings.TrimSpace(expectedToVersion); expected != "" && expected != strings.TrimSpace(tx.ToVersion) {
		return UpdateRollbackResult{}, nil
	}
	if expected := strings.TrimSpace(expectedCreatedAt); expected != "" && expected != strings.TrimSpace(tx.CreatedAt) {
		return UpdateRollbackResult{}, nil
	}
	if expected := strings.TrimSpace(expectedTransactionID); expected != "" && expected != UpdateTransactionID(tx) {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: pending transaction changed")
	}
	return rollbackPendingUpdateMatching(
		tx.ToVersion,
		tx.CreatedAt,
		stateID,
		states,
		UpdateTransactionID(tx),
		false,
	)
}

func rollbackPendingUpdateMatching(
	expectedToVersion, expectedCreatedAt, expectedStateID string,
	expectedStates map[string]string,
	expectedTransactionID string,
	callerConfirmedState bool,
) (UpdateRollbackResult, error) {
	// The expected-match checks below re-run under the strict lock, so a
	// transaction committed, cancelled, or replaced while waiting here is never
	// acted upon.
	unlock, err := acquirePendingUpdateLock()
	if err != nil {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: lock pending transaction: %w", err)
	}
	defer unlock()
	return rollbackPendingUpdateMatchingLocked(
		expectedToVersion,
		expectedCreatedAt,
		expectedStateID,
		expectedStates,
		expectedTransactionID,
		callerConfirmedState,
	)
}

// rollbackPendingUpdateMatchingLocked performs the transition while the caller
// holds the pending-update lock. RecoverFailedInstall uses this form so failure
// marker correlation, rollback, and marker cleanup are one serialized state
// transition.
func rollbackPendingUpdateMatchingLocked(
	expectedToVersion, expectedCreatedAt, expectedStateID string,
	expectedStates map[string]string,
	expectedTransactionID string,
	callerConfirmedState bool,
) (UpdateRollbackResult, error) {
	tx, err := ReadPendingUpdate()
	if err != nil {
		if os.IsNotExist(err) {
			return UpdateRollbackResult{}, nil
		}
		return UpdateRollbackResult{}, err
	}
	if expected := strings.TrimSpace(expectedToVersion); expected != "" && expected != strings.TrimSpace(tx.ToVersion) {
		return UpdateRollbackResult{}, nil
	}
	if expected := strings.TrimSpace(expectedCreatedAt); expected != "" && expected != strings.TrimSpace(tx.CreatedAt) {
		return UpdateRollbackResult{}, nil
	}
	if expected := strings.TrimSpace(expectedTransactionID); expected != "" && expected != repairPlanStateID(tx) {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: pending transaction changed")
	}
	hasBoundState := strings.TrimSpace(expectedStateID) != ""
	if !hasBoundState {
		expectedStateID, expectedStates = pendingUpdateBoundPreview(tx)
	}
	// Share release-unit target locks with other repair mutations so two
	// DEEPZORA_HOME profiles cannot quarantine or restore the same binaries
	// through different pending-update locks.
	unlockTargets, lockErr := lockRepairMutations(pendingUpdateTargetPaths(tx)...)
	if lockErr != nil {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: lock targets: %w", lockErr)
	}
	defer unlockTargets()
	current, err := ReadPendingUpdate()
	if err != nil {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: re-read pending transaction: %w", err)
	}
	if !reflect.DeepEqual(tx, current) {
		return UpdateRollbackResult{}, fmt.Errorf("rollback update: pending transaction changed while waiting")
	}
	tx = current
	verifyBoundState := func() error {
		expected := strings.TrimSpace(expectedStateID)
		if expected == "" {
			return nil
		}
		actual, _ := pendingUpdateBoundPreview(tx)
		if expected != actual {
			return fmt.Errorf("repair plan preview changed since confirmation; re-preview and re-confirm (expected %s, got %s)", expected, actual)
		}
		return nil
	}
	if expected := strings.TrimSpace(expectedStateID); expected != "" {
		if err := verifyBoundState(); err != nil {
			if callerConfirmedState {
				return UpdateRollbackResult{}, nil
			}
			return UpdateRollbackResult{}, err
		}
	}
	confirmedStates := expectedStates
	if !callerConfirmedState {
		// Invocation-local binding proves that the live unit did not drift while
		// this rollback waited for locks. It does not prove that an unbound live
		// node belongs to the pending transaction and therefore cannot authorize
		// deleting the retained aside after restore.
		confirmedStates = nil
	}
	result := UpdateRollbackResult{FromVersion: tx.ToVersion, ToVersion: tx.FromVersion, TargetPath: tx.TargetPath}
	var verifyCommitState func() error
	switch tx.TargetKind {
	case "app-bundle":
		if strings.TrimSpace(tx.BackupTreeID) == "" {
			return result, fmt.Errorf("rollback update: backup bundle identity is missing")
		}
		backupInfo, err := os.Lstat(tx.BackupPath)
		if err != nil {
			if os.IsNotExist(err) && strings.TrimSpace(tx.BackupTreeID) != "" {
				actual, digestErr := repairPlanTreeContentStateID(tx.TargetPath)
				if digestErr == nil && actual == tx.BackupTreeID {
					result.RolledBack = true
					if removeErr := removePendingUpdateExactVerified(tx, func() error {
						current, currentErr := repairPlanTreeContentStateID(tx.TargetPath)
						if currentErr != nil || current != tx.BackupTreeID {
							return fmt.Errorf("rollback update: restored bundle changed before commit")
						}
						return nil
					}); removeErr != nil {
						return result, fmt.Errorf("rollback update: clear pending transaction: %w", removeErr)
					}
					return result, nil
				}
			}
			return result, fmt.Errorf("rollback update: backup bundle: %w", err)
		}
		if !backupInfo.IsDir() {
			return result, fmt.Errorf("rollback update: backup bundle is not a directory")
		}
		actual, digestErr := repairPlanTreeContentStateID(tx.BackupPath)
		if digestErr != nil || actual != tx.BackupTreeID {
			return result, fmt.Errorf("rollback update: backup bundle digest mismatch")
		}
		if err := verifyBoundState(); err != nil {
			return result, err
		}
		failed := ""
		retainedFailed := false
		retainedFailedOwned := false
		retainedFailedState := ""
		if _, statErr := os.Lstat(tx.TargetPath); statErr == nil {
			retainedFailedState = repairPlanReleaseNodeState(tx.TargetPath)
			var retainErr error
			failed, retainErr = retainUpdateRollbackNode(tx.TargetPath, "deepzora-failed")
			if retainErr != nil {
				return result, fmt.Errorf("rollback update: move failed bundle: %w", retainErr)
			}
			retainedFailed = true
			if verifyErr := verifyRepairPlanReleaseNodeStateFor(failed, tx.TargetPath, retainedFailedState); verifyErr != nil {
				if restoreErr := rollbackSwapRename(failed, tx.TargetPath); restoreErr != nil {
					result.MixedInstall = true
					return result, fmt.Errorf("%w; preserve moved live bundle at %s: %w", verifyErr, failed, restoreErr)
				}
				return result, verifyErr
			}
			if strings.TrimSpace(tx.HandoffAppTreeID) != "" {
				retainedFailedOwned = VerifyAppBundleUpdateHandoffReplacement(tx, failed) == nil
			}
			if expected := confirmedStates[tx.TargetPath]; expected != "" {
				if verifyErr := verifyRepairPlanReleaseNodeStateFor(failed, tx.TargetPath, expected); verifyErr != nil {
					if restoreErr := rollbackSwapRename(failed, tx.TargetPath); restoreErr != nil {
						result.MixedInstall = true
						return result, fmt.Errorf("%w; preserve moved live bundle at %s: %w", verifyErr, failed, restoreErr)
					}
					return result, verifyErr
				}
				retainedFailedOwned = true
			}
		} else if !os.IsNotExist(statErr) {
			return result, fmt.Errorf("rollback update: inspect live bundle: %w", statErr)
		}
		if _, statErr := os.Lstat(tx.TargetPath); statErr == nil {
			result.MixedInstall = retainedFailed
			return result, fmt.Errorf("rollback update: target bundle was recreated before restore")
		} else if !os.IsNotExist(statErr) {
			result.MixedInstall = retainedFailed
			return result, fmt.Errorf("rollback update: inspect restore target: %w", statErr)
		}
		if err := rollbackSwapRename(tx.BackupPath, tx.TargetPath); err != nil {
			if retainedFailed {
				if verifyErr := verifyRepairPlanReleaseNodeStateFor(failed, tx.TargetPath, retainedFailedState); verifyErr != nil {
					result.MixedInstall = true
					return result, fmt.Errorf("rollback update: restore bundle: %w (retained live bundle changed at %s: %w)", err, failed, verifyErr)
				}
				if restoreErr := rollbackSwapRename(failed, tx.TargetPath); restoreErr != nil {
					result.MixedInstall = true
					return result, fmt.Errorf("rollback update: restore bundle: %w (preserve replacement at %s: %w)", err, failed, restoreErr)
				}
			}
			return result, fmt.Errorf("rollback update: restore bundle: %w", err)
		}
		restoredTreeID, digestErr := repairPlanTreeContentStateID(tx.TargetPath)
		restoredMatches := digestErr == nil && restoredTreeID == tx.BackupTreeID
		if !restoredMatches {
			mismatchErr := fmt.Errorf("rollback update: restored bundle digest mismatch")
			rejected, moveErr := moveRepairNodeToUniqueCleanup(tx.TargetPath)
			if moveErr != nil || rejected == "" {
				result.MixedInstall = true
				if moveErr != nil {
					return result, fmt.Errorf("%w; retain rejected bundle: %w", mismatchErr, moveErr)
				}
				return result, fmt.Errorf("%w; rejected bundle disappeared before compensation", mismatchErr)
			}
			if !retainedFailed {
				result.MixedInstall = true
				return result, fmt.Errorf("%w; rejected bundle retained at %s and no prior live bundle is available", mismatchErr, rejected)
			}
			if verifyErr := verifyRepairPlanReleaseNodeStateFor(failed, tx.TargetPath, retainedFailedState); verifyErr != nil {
				result.MixedInstall = true
				return result, fmt.Errorf("%w; rejected bundle retained at %s; prior live bundle changed at %s: %w", mismatchErr, rejected, failed, verifyErr)
			}
			if restoreErr := rollbackSwapRename(failed, tx.TargetPath); restoreErr != nil {
				result.MixedInstall = true
				return result, fmt.Errorf("%w; rejected bundle retained at %s; restore prior live bundle: %w", mismatchErr, rejected, restoreErr)
			}
			if verifyErr := verifyRepairPlanReleaseNodeStateFor(tx.TargetPath, tx.TargetPath, retainedFailedState); verifyErr != nil {
				result.MixedInstall = true
				return result, fmt.Errorf("%w; rejected bundle retained at %s; restored prior live bundle changed: %w", mismatchErr, rejected, verifyErr)
			}
			return result, fmt.Errorf("%w; rejected bundle retained at %s and prior live bundle restored", mismatchErr, rejected)
		}
		verifyCommitState = func() error {
			current, currentErr := repairPlanTreeContentStateID(tx.TargetPath)
			if currentErr != nil {
				return fmt.Errorf("rollback update: read restored bundle before commit: %w", currentErr)
			}
			if current != restoredTreeID {
				return fmt.Errorf("rollback update: restored bundle changed before commit")
			}
			return nil
		}
		if retainedFailed && retainedFailedOwned {
			_ = removeUpdateNodeMatching(failed, func(moved string) error {
				return verifyRepairPlanReleaseNodeStateFor(moved, tx.TargetPath, retainedFailedState)
			}, true)
		}
	default:
		return result, fmt.Errorf("rollback update: unsupported target kind %q", tx.TargetKind)
	}
	if err := verifyCommitState(); err != nil {
		return result, err
	}
	result.RolledBack = true
	if err := removePendingUpdateExactVerified(tx, verifyCommitState); err != nil {
		return result, fmt.Errorf("rollback update: clear pending transaction: %w", err)
	}

	return result, nil
}

func retainUpdateRollbackNode(path, suffix string) (string, error) {
	for attempt := range 16 {
		retained := fmt.Sprintf(
			"%s.%s-%d-%d",
			path,
			suffix,
			time.Now().UTC().UnixNano(),
			attempt,
		)
		if err := rollbackSwapRename(path, retained); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		return retained, nil
	}
	return "", fmt.Errorf("cannot allocate retained update path")
}

// Bundle rename and cleanup indirection for failure injection.
var (
	rollbackSwapRename         = renameRepairNodeNoReplace
	removePendingUpdateFile    = os.Remove
	pendingUpdateBeforeCleanup = func(string) {}
	updateCleanupAfterRename   = func(string, string) {}
)

func validateUpdateTransaction(tx *UpdateTransaction) error {
	if tx == nil || tx.SchemaVersion != updateTransactionVersion || strings.TrimSpace(tx.ToVersion) == "" {
		return fmt.Errorf("pending update metadata is incomplete")
	}
	launcher, err := repairExecutable()
	if err != nil {
		return fmt.Errorf("pending update launcher path is unavailable")
	}
	return validateUpdateTransactionForLauncher(tx, launcher)
}

func validateUpdateTransactionForLauncher(tx *UpdateTransaction, launcher string) error {
	if tx == nil || tx.SchemaVersion != updateTransactionVersion || strings.TrimSpace(tx.ToVersion) == "" {
		return fmt.Errorf("pending update metadata is incomplete")
	}
	if strings.TrimSpace(tx.Platform) == "" || strings.TrimSpace(tx.CreatedAt) == "" {
		return fmt.Errorf("pending update transaction identity is incomplete")
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(tx.CreatedAt)); err != nil {
		return fmt.Errorf("pending update creation identity is invalid")
	}
	tx.TargetPath = filepath.Clean(tx.TargetPath)
	tx.BackupPath = filepath.Clean(tx.BackupPath)
	launcher = filepath.Clean(strings.TrimSpace(launcher))
	if launcher == "" || launcher == "." {
		return fmt.Errorf("pending update launcher path is unavailable")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(launcher); resolveErr == nil {
		launcher = resolved
	}
	launcher = filepath.Clean(launcher)
	switch tx.TargetKind {
	case "app-bundle":
		if !strings.HasSuffix(strings.ToLower(tx.TargetPath), ".app") || tx.BackupPath != tx.TargetPath+".deepzora-update-backup" {
			return fmt.Errorf("pending update bundle paths are invalid")
		}
		inside := tx.TargetPath + string(filepath.Separator)
		if !strings.HasPrefix(launcher, inside) {
			return fmt.Errorf("%w: pending update bundle is not the current desktop installation", errPendingUpdateForeignInstall)
		}
		if err := validateAppBundleHandoffMetadata(tx); err != nil {
			return fmt.Errorf("pending update %w", err)
		}
		if len(tx.BackupTreeID) != sha256.Size*2 {
			return fmt.Errorf("pending update backup bundle digest is missing or invalid")
		}
		if _, err := hex.DecodeString(tx.BackupTreeID); err != nil {
			return fmt.Errorf("pending update backup bundle digest is invalid")
		}
	default:
		return fmt.Errorf("pending update target kind is invalid")
	}
	return nil
}

func pathInsideResolvedRoot(root, path string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	path = filepath.Clean(strings.TrimSpace(path))
	if root == "" || path == "" {
		return false
	}
	lexicalRel, err := filepath.Rel(root, path)
	if err != nil || lexicalRel == ".." || strings.HasPrefix(lexicalRel, ".."+string(filepath.Separator)) {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolvedRel, err := filepath.Rel(resolvedRoot, resolvedPath)
	return err == nil && resolvedRel != ".." &&
		!strings.HasPrefix(resolvedRel, ".."+string(filepath.Separator))
}

func validateAppBundleHandoffMetadata(tx *UpdateTransaction) error {
	if tx == nil {
		return fmt.Errorf("handoff metadata is incomplete")
	}
	hasAny := strings.TrimSpace(tx.HandoffAppPath) != "" ||
		strings.TrimSpace(tx.HandoffStagingPath) != "" ||
		strings.TrimSpace(tx.HandoffAppTreeID) != "" ||
		strings.TrimSpace(tx.HandoffStagingTreeID) != "" ||
		tx.HandoffOwnerPID != 0
	if !hasAny {
		return nil
	}
	tx.HandoffAppPath = filepath.Clean(strings.TrimSpace(tx.HandoffAppPath))
	tx.HandoffStagingPath = filepath.Clean(strings.TrimSpace(tx.HandoffStagingPath))
	if tx.HandoffOwnerPID <= 0 ||
		!filepath.IsAbs(tx.HandoffAppPath) ||
		!filepath.IsAbs(tx.HandoffStagingPath) ||
		!strings.HasSuffix(strings.ToLower(tx.HandoffAppPath), ".app") {
		return fmt.Errorf("handoff metadata is incomplete")
	}
	if tx.HandoffAppPath == tx.TargetPath || tx.HandoffAppPath == tx.BackupPath {
		return fmt.Errorf("handoff app overlaps the installed bundle")
	}
	rel, err := filepath.Rel(tx.HandoffStagingPath, tx.HandoffAppPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("handoff app is outside its staging directory")
	}
	tempRoot := filepath.Clean(os.TempDir())
	stagingRel, err := filepath.Rel(tempRoot, tx.HandoffStagingPath)
	if err != nil || stagingRel == "." || stagingRel == ".." || strings.HasPrefix(stagingRel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("handoff staging directory is outside the system temporary directory")
	}
	stagingBase := strings.Split(stagingRel, string(filepath.Separator))[0]
	if !strings.HasPrefix(stagingBase, "deepzora-mac-update-") {
		return fmt.Errorf("handoff staging directory has an unexpected name")
	}
	return nil
}
