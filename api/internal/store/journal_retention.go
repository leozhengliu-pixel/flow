package store

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// The legacy storage sweep runs hourly. Journal retention checks its byte
// budget every minute, independently, using bounded pruning transactions.
func (s *SQLiteStore) startRecordJournalMaintenance() {
	if !s.db.journalReady.Load() || recordJournalMode(s.dialect) == "off" {
		return
	}
	ctx, cancel := context.WithCancel(s.WorkerContext())
	s.db.journalStop = cancel
	s.db.journalWorkers.Add(1)
	go func() {
		defer s.db.journalWorkers.Done()
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-timer.C:
				pass, stop := context.WithTimeout(ctx, 10*time.Second)
				err := s.pruneRecordJournalRetention(pass, now.Add(-MinChangeLogRetention))
				stop()
				if err != nil && ctx.Err() == nil {
					slog.Warn("record journal retention deferred", "error", err)
				}
			}
		}
	}()
}

func (d *sqlDatabase) Close() error {
	d.stopRecordJournalMaintenance()
	return d.closePools()
}

func (d *sqlDatabase) stopRecordJournalMaintenance() {
	if d.journalStop != nil {
		d.journalStop()
	}
	d.journalWorkers.Wait()
}

func (d *sqlDatabase) closePools() error {
	var snapshotErr, readErr error
	if d.snapshots != nil {
		snapshotErr = d.snapshots.Close()
	}
	if d.sqliteReads != nil {
		readErr = d.sqliteReads.Close()
	}
	return errors.Join(snapshotErr, readErr, d.DB.Close())
}
