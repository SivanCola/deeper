package repair

import "deepzora/internal/fileutil"

func renameRepairNodeNoReplace(oldPath, newPath string) error {
	return fileutil.RenameNoReplace(oldPath, newPath)
}
