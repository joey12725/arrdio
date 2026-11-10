package jobs

import (
	"context"
	"errors"
	"strings"
)

func (r *Runtime) SealTransferURL(value string) ([]byte, error) {
	return r.seal([]byte("transfer-url:" + value))
}
func (r *Runtime) OpenTransferURL(value []byte) (string, error) {
	plain, e := r.unseal(value)
	if e != nil {
		return "", e
	}
	if !strings.HasPrefix(string(plain), "transfer-url:") {
		return "", errors.New("invalid transfer credential")
	}
	return strings.TrimPrefix(string(plain), "transfer-url:"), nil
}

type checkpointKey struct{}
type TransferCheckpoint func(Receipt) error

func WithTransferCheckpoint(ctx context.Context, save TransferCheckpoint) context.Context {
	return context.WithValue(ctx, checkpointKey{}, save)
}
func HasTransferCheckpoint(ctx context.Context) bool {
	_, ok := ctx.Value(checkpointKey{}).(TransferCheckpoint)
	return ok
}
func CheckpointTransfer(ctx context.Context, receipt Receipt) error {
	save, ok := ctx.Value(checkpointKey{}).(TransferCheckpoint)
	if !ok {
		return errors.New("durable transfer checkpoint is required")
	}
	return save(receipt)
}

type FinalTransferFailure struct{ Err error }

var ErrUnresolvedTransfer = errors.New("transfer outcome unresolved; existing intent retained")

// ErrPayloadPending means the source finished but its files are not readable on
// the payload mount yet. It concerns one job, never the provider's health.
var ErrPayloadPending = errors.New("download complete; files not readable yet")

func (e FinalTransferFailure) Error() string { return e.Err.Error() }
func (e FinalTransferFailure) Unwrap() error { return e.Err }

type RetryTransferFailure struct{ Err error }

// UnusableCandidate means a finished transfer's content can never satisfy the
// job: the pack lacks the requested volume, no file is readable, or the audio
// cannot be decoded. The candidate is set aside for a long time and another is
// chosen; the provider itself is healthy.
type UnusableCandidate struct{ Err error }

func (e UnusableCandidate) Error() string { return e.Err.Error() }
func (e UnusableCandidate) Unwrap() error { return e.Err }

func (e RetryTransferFailure) Error() string { return e.Err.Error() }
func (e RetryTransferFailure) Unwrap() error { return e.Err }

type RetainedFile struct {
	Identity string `json:"identity"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
}
type retainedFilesKey struct{}

func RetainedFiles(ctx context.Context) []RetainedFile {
	files, _ := ctx.Value(retainedFilesKey{}).([]RetainedFile)
	return files
}
func WithRetainedFiles(ctx context.Context, files []RetainedFile) context.Context {
	return context.WithValue(ctx, retainedFilesKey{}, files)
}

type orderedTracksKey struct{}

func WithOrderedTracks(ctx context.Context, ordered bool) context.Context {
	return context.WithValue(ctx, orderedTracksKey{}, ordered)
}
func OrderedTracks(ctx context.Context) bool {
	ordered, _ := ctx.Value(orderedTracksKey{}).(bool)
	return ordered
}
