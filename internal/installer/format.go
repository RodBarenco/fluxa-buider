// Package installer creates platform-native installers from verified portable results.
package installer

import (
	"context"

	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

// Request is the target-independent input accepted by an installer format.
type Request struct {
	OutputDir   string
	ProjectName string
	ProjectID   string
	Version     string
	Terminal    bool
	Portable    portable.Result
}

// Result identifies one verified installer artifact.
type Result struct {
	Path         string
	ChecksumPath string
	SHA256       string
	Size         int64
	Format       string
}

// Format is one independently testable native installer backend.
type Format interface {
	Name() string
	Build(context.Context, Request) (Result, error)
}
