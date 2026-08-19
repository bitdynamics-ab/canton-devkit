package localnet

import "testing"

func TestListHasLsAlias(t *testing.T) {
	cmd := buildList()
	for _, alias := range cmd.Aliases {
		if alias == "ls" {
			return
		}
	}
	t.Fatal("list command must have the `ls` alias")
}
