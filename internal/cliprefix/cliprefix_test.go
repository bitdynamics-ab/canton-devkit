package cliprefix

import "testing"

func TestGet_DefaultsToDPM(t *testing.T) {
	if got := Get(); got != "dpm localnet" {
		t.Fatalf("Get() with nothing set = %q, want %q", got, "dpm localnet")
	}
	if Default != "dpm localnet" {
		t.Errorf("Default = %q, want %q", Default, "dpm localnet")
	}
	if got := Cmd("list"); got != "dpm localnet list" {
		t.Errorf("Cmd(list) = %q, want %q", got, "dpm localnet list")
	}
}

func TestSet_OverridesAndRestores(t *testing.T) {
	restore := Set("canton-devkit localnet")
	if got := Get(); got != "canton-devkit localnet" {
		t.Errorf("Get() after Set = %q", got)
	}
	if got := Cmd("down demo"); got != "canton-devkit localnet down demo" {
		t.Errorf("Cmd(down demo) = %q", got)
	}

	inner := Set("dpm localnet")
	if got := Get(); got != "dpm localnet" {
		t.Errorf("nested Set: Get() = %q", got)
	}
	inner()
	if got := Get(); got != "canton-devkit localnet" {
		t.Errorf("inner restore must bring back the outer prefix, got %q", got)
	}

	restore()
	if got := Get(); got != Default {
		t.Errorf("restore must bring back the default, got %q", got)
	}
}

func TestSet_EmptyFallsBackToDefault(t *testing.T) {
	t.Cleanup(Set(""))
	if got := Get(); got != Default {
		t.Errorf("Get() after Set(\"\") = %q, want %q", got, Default)
	}
}
