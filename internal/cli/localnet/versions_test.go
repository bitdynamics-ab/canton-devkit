package localnet

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bitdynamics-ab/canton-devkit/internal/splice"
)

func TestRenderVersions_TextShowsChannelAndImageRepo(t *testing.T) {
	rows := []splice.VersionStatus{
		{
			Tag:              "0.6.4",
			Status:           splice.StatusSupported,
			CataloguedCommit: "578b7822d62947763a48334d556aefebc7ffacec",
			Major:            "0.6",
		},
		{
			Tag:              "token-standard-v2",
			Status:           splice.StatusSupported,
			CataloguedCommit: "de911e38af78ec79b6f0e6a9515e104b1e4c3d62",
			Major:            "0.6",
			Channel:          "alpha",
			ImageRepo:        "ghcr.io/digital-asset/decentralized-canton-sync-dev/docker",
		},
	}

	var out bytes.Buffer
	if err := renderVersions(&out, rows, "text"); err != nil {
		t.Fatalf("renderVersions: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"TAG",
		"CHANNEL",
		"image_repo=ghcr.io/digital-asset/decentralized-canton-sync-dev/docker",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text output missing %q\nfull:\n%s", want, text)
		}
	}
	if !lineContainsFields(text, "0.6.4", "supported", "stable") {
		t.Errorf("0.6.4 row missing stable channel\nfull:\n%s", text)
	}
	if !lineContainsFields(text, "token-standard-v2", "supported", "alpha") {
		t.Errorf("token-standard-v2 row missing alpha channel\nfull:\n%s", text)
	}
}

func TestRenderVersions_JSONIncludesChannelAndImageRepo(t *testing.T) {
	rows := []splice.VersionStatus{{
		Tag:              "token-standard-v2",
		Status:           splice.StatusSupported,
		CataloguedCommit: "de911e38af78ec79b6f0e6a9515e104b1e4c3d62",
		Major:            "0.6",
		Channel:          "alpha",
		ImageRepo:        "ghcr.io/digital-asset/decentralized-canton-sync-dev/docker",
	}}

	var out bytes.Buffer
	if err := renderVersions(&out, rows, "json"); err != nil {
		t.Fatalf("renderVersions: %v", err)
	}
	var got []struct {
		Tag              string
		Status           string
		CataloguedCommit string
		Major            string
		Channel          string
		ImageRepo        string
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\n%s", err, out.String())
	}
	if len(got) != 1 {
		t.Fatalf("decoded %d rows, want 1", len(got))
	}
	if got[0].Channel != "alpha" {
		t.Errorf("Channel = %q, want alpha", got[0].Channel)
	}
	if got[0].ImageRepo != "ghcr.io/digital-asset/decentralized-canton-sync-dev/docker" {
		t.Errorf("ImageRepo = %q", got[0].ImageRepo)
	}
}

func TestRenderCatalogueOnly_PropagatesAlphaMetadata(t *testing.T) {
	var out bytes.Buffer
	if err := renderCatalogueOnly(&out, "json"); err != nil {
		t.Fatalf("renderCatalogueOnly: %v", err)
	}
	var rows []struct {
		Tag       string
		Channel   string
		ImageRepo string
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\n%s", err, out.String())
	}

	for _, row := range rows {
		if row.Tag != "token-standard-v2" {
			continue
		}
		if row.Channel != "alpha" {
			t.Errorf("Channel = %q, want alpha", row.Channel)
		}
		if row.ImageRepo != "ghcr.io/digital-asset/decentralized-canton-sync-dev/docker" {
			t.Errorf("ImageRepo = %q", row.ImageRepo)
		}
		return
	}
	t.Fatal("token-standard-v2 missing from catalogue-only output")
}

func lineContainsFields(text string, fields ...string) bool {
	for _, line := range strings.Split(text, "\n") {
		ok := true
		for _, field := range fields {
			if !strings.Contains(line, field) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
