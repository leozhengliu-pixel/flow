package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

var errHydratedEncodingPage = errors.New("hydrated encoding page is full")

type hydratedEncodingPage struct{ bytes.Buffer }

func (p *hydratedEncodingPage) Write(value []byte) (int, error) {
	if len(value) > (256<<10)-p.Len() {
		return 0, errHydratedEncodingPage
	}
	return p.Buffer.Write(value)
}

func (p *hydratedEncodingPage) WriteString(value string) (int, error) {
	if len(value) > (256<<10)-p.Len() {
		return 0, errHydratedEncodingPage
	}
	return p.Buffer.WriteString(value)
}

// Four bounded pages share the CPU work of one admitted download. Pages are
// emitted in source order; an oversized page falls back to the direct encoder.
func encodeHydratedProjectArray(ctx context.Context, out *bufio.Writer, value reflect.Value, depth int) error {
	if value.IsNil() {
		_, err := out.WriteString("null")
		return err
	}
	if value.Len() == 0 {
		_, err := out.WriteString("[]")
		return err
	}
	type job struct{ start, end int }
	type worker struct {
		jobs chan job
		done chan error
		page hydratedEncodingPage
	}
	ctx, cancel := context.WithCancel(ctx)
	var running sync.WaitGroup
	workers := make([]worker, 4)
	for i := range workers {
		w := &workers[i]
		w.jobs, w.done = make(chan job), make(chan error)
		w.page.Grow(256 << 10)
		running.Add(1)
		go func() {
			defer running.Done()
			buffer := bufio.NewWriterSize(&w.page, 32<<10)
			for {
				select {
				case <-ctx.Done():
					return
				case task := <-w.jobs:
					w.page.Reset()
					buffer.Reset(&w.page)
					var err error
					for index := task.start; index < task.end && err == nil; index++ {
						if index > task.start {
							buffer.WriteByte(',')
						}
						err = encodeHydratedJSON(ctx, buffer, value.Index(index), depth+1)
					}
					if err == nil {
						err = buffer.Flush()
					}
					select {
					case w.done <- err:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	defer func() { cancel(); running.Wait() }()
	out.WriteByte('[')
	pageRecords := 32
	var bodyBytes int
	for i := 0; i < min(64, value.Len()); i++ {
		bodyBytes += len(value.Index(i).Interface().(domain.Project).Description)
	}
	if average := bodyBytes / min(64, value.Len()); average > 0 {
		pageRecords = max(1, min(pageRecords, (192<<10)/(average+1024)))
	}
	for start := 0; start < value.Len(); start += pageRecords * len(workers) {
		active := min(len(workers), (value.Len()-start+pageRecords-1)/pageRecords)
		for i := range active {
			task := job{start + i*pageRecords, min(start+(i+1)*pageRecords, value.Len())}
			select {
			case workers[i].jobs <- task:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		for i := range active {
			var err error
			select {
			case err = <-workers[i].done:
			case <-ctx.Done():
				return ctx.Err()
			}
			first := start + i*pageRecords
			if first > 0 {
				out.WriteByte(',')
			}
			if errors.Is(err, errHydratedEncodingPage) {
				for index := first; index < min(first+pageRecords, value.Len()); index++ {
					if index > first {
						out.WriteByte(',')
					}
					if err := encodeHydratedJSON(ctx, out, value.Index(index), depth+1); err != nil {
						return err
					}
				}
			} else if err != nil {
				return err
			} else {
				page := workers[i].page.Bytes()
				for len(page) > 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
					size := min(len(page), 32<<10)
					if _, err := out.Write(page[:size]); err != nil {
						return err
					}
					page = page[size:]
				}
			}
		}
	}
	return out.WriteByte(']')
}
