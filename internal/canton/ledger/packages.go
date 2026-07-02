package ledger

import (
	"context"
	"fmt"

	lapiv2 "github.com/digital-asset/dazl-client/v8/go/api/com/daml/ledger/api/v2"
)

// ListPackages lists the package IDs currently loaded on the participant.
// Used by the DAR Web UI's package explorer and `dar list`-style CLI.
//
// Note: this is the read-only PackageService. For admin operations
// (upload, vetting changes), use [Client.UploadDarFile] etc. on the
// admin PackageManagementService — wired in admin.go.
func (c *Client) ListPackages(ctx context.Context) (*lapiv2.ListPackagesResponse, error) {
	resp, err := c.pkg.ListPackages(ctx, &lapiv2.ListPackagesRequest{})
	if err != nil {
		return nil, fmt.Errorf("ledger.ListPackages: %w", err)
	}
	return resp, nil
}

// GetPackage fetches the bytes of a specific Daml-LF package by its
// package ID. Used by the package explorer to decode contract payloads
// into typed form: the response Payload is the raw archive bytes, which
// the explorer's decoder passes to the daml-lf reader for
// module/template extraction.
func (c *Client) GetPackage(ctx context.Context, packageId string) (*lapiv2.GetPackageResponse, error) {
	resp, err := c.pkg.GetPackage(ctx, &lapiv2.GetPackageRequest{PackageId: packageId})
	if err != nil {
		return nil, fmt.Errorf("ledger.GetPackage(%q): %w", packageId, err)
	}
	return resp, nil
}

// GetPackageStatus reports whether a package ID is REGISTERED on the
// participant. Cheaper than GetPackage when the caller only needs
// presence (e.g. "is the workflow's main DAR vetted here yet?").
func (c *Client) GetPackageStatus(ctx context.Context, packageId string) (*lapiv2.GetPackageStatusResponse, error) {
	resp, err := c.pkg.GetPackageStatus(ctx, &lapiv2.GetPackageStatusRequest{PackageId: packageId})
	if err != nil {
		return nil, fmt.Errorf("ledger.GetPackageStatus(%q): %w", packageId, err)
	}
	return resp, nil
}
