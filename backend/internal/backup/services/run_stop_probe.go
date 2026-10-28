package services

import "sync"

type backupRunStopProbe func(runID uint64) (bool, error)

var (
	backupRunStopProbeMu sync.RWMutex
	backupRunStopProbeFn backupRunStopProbe
)

func SetBackupRunStopProbe(fn func(runID uint64) (bool, error)) {
	backupRunStopProbeMu.Lock()
	defer backupRunStopProbeMu.Unlock()
	backupRunStopProbeFn = fn
}

func resolveBackupRunStopRequested(runID uint64) bool {
	if runID == 0 {
		return false
	}
	if globalStopRequested := isBackupRunStopRequestedFromDB(runID); globalStopRequested {
		return true
	}
	backupRunStopProbeMu.RLock()
	fn := backupRunStopProbeFn
	backupRunStopProbeMu.RUnlock()
	if fn == nil {
		return false
	}
	stopRequested, err := fn(runID)
	if err != nil {
		return false
	}
	return stopRequested
}
