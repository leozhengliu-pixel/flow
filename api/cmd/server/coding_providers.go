package main

// Sandbox providers and code hosts register themselves in init; importing
// them here makes them available to sandbox.Open and codehost.OpenAll.
import (
	_ "flow/api/internal/codehost/github"
	_ "flow/api/internal/codehost/gitlab"
	_ "flow/api/internal/sandbox/docker"
	_ "flow/api/internal/sandbox/kubernetes"
)
