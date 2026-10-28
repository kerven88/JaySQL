package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/global"
)

var ErrBackupRunStopped = errors.New("backup run stopped")

func IsBackupRunStoppedError(err error) bool {
	return errors.Is(err, ErrBackupRunStopped)
}

type backupRunControl struct {
	runID          uint64
	cancel         context.CancelFunc
	stopRequested  atomic.Bool
	stopLoggedOnce atomic.Bool
	stopHandlersMu sync.Mutex
	stopHandlers   []func()
	stopOnce       sync.Once
}

func attachBackupRunControl(parent context.Context, runID uint64, logFile backupRunLogWriter) (context.Context, *backupRunControl) {
	ctx, cancel := context.WithCancel(parent)
	control := &backupRunControl{
		runID:  runID,
		cancel: cancel,
	}
	if runID == 0 {
		return ctx, control
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !resolveBackupRunStopRequested(runID) {
					continue
				}
				if control.stopRequested.CompareAndSwap(false, true) && logFile != nil {
					logFile.Warnf("检测到停止请求，准备中止当前备份进程")
				}
				cancel()
				control.runStopHandlers(logFile)
				return
			}
		}
	}()
	return ctx, control
}

func (c *backupRunControl) Close() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
}

func (c *backupRunControl) StopRequested() bool {
	return c != nil && c.stopRequested.Load()
}

func (c *backupRunControl) AddStopHandler(fn func()) {
	if c == nil || fn == nil {
		return
	}
	if c.StopRequested() {
		go safeRunBackupStopHandler(fn)
		return
	}
	c.stopHandlersMu.Lock()
	c.stopHandlers = append(c.stopHandlers, fn)
	c.stopHandlersMu.Unlock()
	if c.StopRequested() {
		c.runStopHandlers(nil)
	}
}

func (c *backupRunControl) runStopHandlers(logFile backupRunLogWriter) {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		c.stopHandlersMu.Lock()
		handlers := append([]func(){}, c.stopHandlers...)
		c.stopHandlersMu.Unlock()
		for _, fn := range handlers {
			safeRunBackupStopHandler(fn)
		}
		if len(handlers) > 0 && logFile != nil {
			logFile.Warnf("已触发备份停止清理回调，等待远端进程退出")
		}
	})
}

func safeRunBackupStopHandler(fn func()) {
	defer func() {
		_ = recover()
	}()
	fn()
}

func (c *backupRunControl) Checkpoint(logFile backupRunLogWriter) error {
	if c == nil || !c.StopRequested() {
		return nil
	}
	if c.stopLoggedOnce.CompareAndSwap(false, true) && logFile != nil {
		logFile.Warnf("本次备份已按停止请求终止，后续不再继续归档或写入备份集")
	}
	return ErrBackupRunStopped
}

func isBackupRunStopRequestedFromDB(runID uint64) bool {
	if runID == 0 || global.App.DB == nil {
		return false
	}
	var row struct {
		Status string
	}
	if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
		Select("status").
		Where("id = ?", runID).
		Take(&row).Error; err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(row.Status), "STOP_REQUESTED")
}

func isBackupRunStatusRunning(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PENDING", "RUNNING", "STOP_REQUESTED":
		return true
	default:
		return false
	}
}
