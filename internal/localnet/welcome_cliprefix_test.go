package localnet

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/cliprefix"
	"github.com/bitdynamics-ab/canton-devkit/internal/registry"
)

func TestRenderWelcome_NamesTheLaunchCommand(t *testing.T) {
	for _, tc := range []struct {
		prefix  string
		want    string
		notWant string
	}{
		{"", "dpm localnet", "canton-devkit localnet"},
		{"canton-devkit localnet", "canton-devkit localnet", "dpm localnet"},
	} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			t.Setenv("CANTON_DEVKIT_REGISTRY", t.TempDir())
			if tc.prefix != "" {
				t.Cleanup(cliprefix.Set(tc.prefix))
			}
			state := registry.NewState("demo", "0.6.4")
			state.Ports = map[string]int{"app_user_ui": 4441}

			var out bytes.Buffer
			renderWelcome(&out, "demo", "0.6.4", state, 0)
			body := out.String()
			for _, w := range []string{
				"(" + tc.want + " ui)",
				tc.want + " status demo",
				`eval "$(` + tc.want + ` env demo)"`,
				tc.want + " contracts watch --name demo",
				tc.want + " dar upload <path> --name demo",
				tc.want + " down demo",
			} {
				if !strings.Contains(body, w) {
					t.Errorf("welcome screen missing %q:\n%s", w, body)
				}
			}
			if strings.Contains(body, tc.notWant) {
				t.Errorf("welcome screen must not mention %q:\n%s", tc.notWant, body)
			}
		})
	}
}
