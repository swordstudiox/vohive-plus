package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/e911"
)

func TestE911ErrorStatus(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: e911.ErrNotSupported, want: http.StatusBadRequest},
		{err: e911.ErrProviderUnavailable, want: http.StatusBadRequest},
		{err: e911.ErrIdentityUnavailable, want: http.StatusConflict},
		{err: e911.ErrChallengeIncomplete, want: http.StatusNotImplemented},
		{err: e911.ErrCarrierWebsheetAbsent, want: http.StatusBadGateway},
		{err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		if got := e911ErrorStatus(tt.err); got != tt.want {
			t.Fatalf("e911ErrorStatus(%v)=%d want %d", tt.err, got, tt.want)
		}
	}
}
