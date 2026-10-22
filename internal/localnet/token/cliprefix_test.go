package token

import (
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
)

func TestDARUnavailableError_NamesTheLaunchCommand(t *testing.T) {
	for _, tc := range []struct {
		prefix  string
		want    []string
		notWant string
	}{
		{"", []string{"`dpm localnet up --version 0.6.12`", "`dpm localnet dar upload`"}, "canton-devkit localnet"},
		{"canton-devkit localnet", []string{"`canton-devkit localnet up --version 0.6.12`", "`canton-devkit localnet dar upload`"}, "dpm localnet"},
	} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			if tc.prefix != "" {
				t.Cleanup(cliprefix.Set(tc.prefix))
			}
			msg := darUnavailableError("splice-test-token-v2", "0.6.4").Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message missing %q: %s", w, msg)
				}
			}
			if strings.Contains(msg, tc.notWant) {
				t.Errorf("message must not mention %q: %s", tc.notWant, msg)
			}
		})
	}
}
