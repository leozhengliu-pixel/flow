package store

import (
	"context"
	"log/slog"
	"time"
)

// The legacy storage sweep runs hourly. Journal retention checks its byte
// budget every minute, independently, using bounded pruning transactions.
func (s *SQLiteStore) startRecordJournalMaintenance() {
	if !s.db.journalReady.Load() || recordJournalMode() == "off" {
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
	if d.journalStop != nil {
		d.journalStop()
	}
	d.journalWorkers.Wait()
	return d.DB.Close()
}
