package sync

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/store"
)

func TestClassifySyncError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		status store.SyncStatus
	}{
		{"auth", &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeAuthenticationFailed}, store.SyncAuthFailed},
		{"authorization", &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeAuthorizationFailed}, store.SyncAuthFailed},
		{"dial", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, store.SyncUnreachable},
		{"other", errors.New("fetch failed"), store.SyncError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code, _ := classifySyncError(tc.err)
			if status != tc.status {
				t.Errorf("status = %q, want %q", status, tc.status)
			}
			if code == "" {
				t.Error("a failure must carry a stable code")
			}
		})
	}
}

func TestClassifySyncErrorBoundsTheDetail(t *testing.T) {
	t.Parallel()
	long := errors.New(strings.Repeat("x", store.MaxSyncErrorDetail+100))
	_, _, detail := classifySyncError(long)
	if len(detail) > store.MaxSyncErrorDetail {
		t.Errorf("detail is %d bytes, over the %d cap", len(detail), store.MaxSyncErrorDetail)
	}
}

func TestCanUseDelta(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		existing store.Folder
		found    bool
		want     bool
	}{
		{"never seen", store.Folder{UIDValidity: 7, HighestModSeq: 3}, false, false},
		{"no modseq", store.Folder{UIDValidity: 7}, true, false},
		{"no validity", store.Folder{HighestModSeq: 3}, true, false},
		{"resumable", store.Folder{UIDValidity: 7, HighestModSeq: 3}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canUseDelta(tc.existing, tc.found); got != tc.want {
				t.Errorf("canUseDelta = %v, want %v", got, tc.want)
			}
		})
	}
}
