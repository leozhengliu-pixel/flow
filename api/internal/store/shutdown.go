package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

const (
	workerShutdownTimeout = 10 * time.Second
	poolCloseTimeout      = 2 * time.Second
)

func (s *SQLiteStore) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), workerShutdownTimeout)
	defer cancel()
	return s.closeWithWorkerDeadline(ctx)
}

func (s *SQLiteStore) closeWithWorkerDeadline(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.workerMu.Lock()
		s.workersClosed = true
		if s.stopLifecycle != nil {
			s.stopLifecycle()
		}
		stops := s.workerShutdowns
		s.workerShutdowns = nil
		s.workerMu.Unlock()

		groups := make([]<-chan struct{}, 0, len(stops))
		for _, stop := range stops {
			groups = append(groups, stop())
		}
		done := make(chan struct{})
		go s.joinWorkers(groups, done)
		var joinErr error
		select {
		case <-done:
		case <-ctx.Done():
			joinErr = ctx.Err()
			slog.Warn("store shutdown timed out waiting for workers", "stacks", shutdownStacks())
		}
		if s.metadataResidency != nil {
			// A bounded store set the process operation work limit at startup.
			setMetadataOperationWork(0)
		}
		// Close the pools even if a worker still holds a workspace lock or
		// borrows metadata. A slow driver must not hold up the other closers.
		pools := make(chan error, 1)
		go func() { pools <- s.db.closePools() }()
		timer := time.NewTimer(poolCloseTimeout)
		defer timer.Stop()
		select {
		case err := <-pools:
			s.closeErr = errors.Join(joinErr, err)
		case <-timer.C:
			slog.Warn("store shutdown timed out closing SQL pools", "stacks", shutdownStacks())
			s.closeErr = context.DeadlineExceeded
		}
	})
	return s.closeErr
}

func (s *SQLiteStore) joinWorkers(groups []<-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for _, group := range groups {
		<-group
	}
	s.workers.Wait()
	s.db.stopRecordJournalMaintenance()
	s.releaseMetadataLogicalSources()
}

// RegisterWorkerShutdown attaches a group that uses this store. The callback
// must cancel the group and immediately return its completion channel. Close
// waits for that channel within its shared worker deadline.
// A false result means Close already started and the caller must not start work.
func (s *SQLiteStore) RegisterWorkerShutdown(stop func() <-chan struct{}) bool {
	s = s.workerOwner()
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workersClosed {
		return false
	}
	s.workerShutdowns = append(s.workerShutdowns, stop)
	return true
}

// GoroutineProfile provides PCs without the arguments or pprof labels that
// textual runtime dumps and profiles may include.
func shutdownStacks() string {
	n, _ := runtime.GoroutineProfile(nil)
	for range 3 {
		records := make([]runtime.StackRecord, n+16)
		count, ok := runtime.GoroutineProfile(records)
		if !ok {
			n = count
			continue
		}
		var stacks strings.Builder
		written := 0
		for _, record := range records[:count] {
			frames := runtime.CallersFrames(record.Stack())
			var stack strings.Builder
			application := false
			for {
				frame, more := frames.Next()
				application = application || strings.HasPrefix(frame.Function, "flow/api/") || strings.HasPrefix(frame.Function, "main.")
				fmt.Fprintf(&stack, "%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
				if !more {
					break
				}
			}
			if application {
				stacks.WriteString(stack.String())
				stacks.WriteByte('\n')
				written++
				if written == 64 {
					stacks.WriteString("additional stacks omitted\n")
					break
				}
			}
		}
		return stacks.String()
	}
	return "goroutine count changed during capture"
}
