package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

func allowedWorkspaceFile(settings domain.WorkspaceSettings, filename, detectedType string) bool {
	if !settings.RestrictFileUploads {
		return true
	}
	if strings.HasPrefix(detectedType, "image/") || strings.HasPrefix(detectedType, "video/") {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	return slices.ContainsFunc(settings.AllowedFileExtensions, func(allowed string) bool {
		return ext != "" && ext == strings.TrimPrefix(strings.ToLower(strings.TrimSpace(allowed)), ".")
	})
}

func (s *server) checkUploadPolicy(workspace, filename string, input io.Reader) (io.Reader, error) {
	metadata, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return nil, fmt.Errorf("workspace not found")
	}
	if !metadata.WorkspaceSettings.RestrictFileUploads {
		return input, nil
	}
	reader := bufio.NewReader(input)
	preview, err := reader.Peek(512)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, err
	}
	if !allowedWorkspaceFile(metadata.WorkspaceSettings, filename, http.DetectContentType(preview)) {
		return nil, fmt.Errorf("file type is not permitted by workspace upload policy")
	}
	return reader, nil
}
