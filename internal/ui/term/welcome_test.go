package term

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
)

func TestWelcomeScreen_DashboardHintNamesTheLaunchCommand(t *testing.T) {
	forcePlain(t)
	for _, tc := range []struct {
		prefix  string
		want    string
		notWant string
	}{
		{"", "(dpm localnet ui)", "canton-devkit localnet"},
		{"canton-devkit localnet", "(canton-devkit localnet ui)", "dpm localnet"},
	} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			if tc.prefix != "" {
				t.Cleanup(cliprefix.Set(tc.prefix))
			}
			s := WelcomeScreen{InstanceName: "demo"}
			for name, render := range map[string]func(*bytes.Buffer){
				"rich":  func(b *bytes.Buffer) { s.renderRich(b) },
				"plain": func(b *bytes.Buffer) { s.renderPlain(b) },
			} {
				var out bytes.Buffer
				render(&out)
				if !strings.Contains(out.String(), tc.want) {
					t.Errorf("%s: missing %q:\n%s", name, tc.want, out.String())
				}
				if strings.Contains(out.String(), tc.notWant) {
					t.Errorf("%s: must not mention %q:\n%s", name, tc.notWant, out.String())
				}
			}
		})
	}
}
