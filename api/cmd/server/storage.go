package main

import (
	"context"
	"sync"

	"flow/api/internal/objectstore"
)

var storageInit sync.Mutex

func (s *server) storage() (objectstore.Store, error) {
	if s.objectStore != nil {
		return s.objectStore, nil
	}
	storageInit.Lock()
	defer storageInit.Unlock()
	if s.objectStore == nil {
		storage, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: s.uploadPath})
		if err != nil {
			return nil, err
		}
		s.objectStore = storage
	}
	return s.objectStore, nil
}
