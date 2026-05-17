package localnet

import "testing"

func TestParseUpArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    *UpOptions
		wantErr bool
	}{
		{
			name:    "valid name only",
			args:    []string{"--name", "my-net"},
			want:    &UpOptions{Name: "my-net", Version: "latest"},
			wantErr: false,
		},
		{
			name:    "name and version",
			args:    []string{"--name", "testnet", "--version", "2.8.0"},
			want:    &UpOptions{Name: "testnet", Version: "2.8.0"},
			wantErr: false,
		},
		{
			name:    "missing name",
			args:    []string{"--version", "2.8.0"},
			wantErr: true,
		},
		{
			name:    "empty args",
			args:    nil,
			wantErr: true,
		},
		{
			name:    "name missing value",
			args:    []string{"--name"},
			wantErr: true,
		},
		{
			name:    "invalid name with spaces",
			args:    []string{"--name", "bad name"},
			wantErr: true,
		},
		{
			name:    "invalid name starts with hyphen",
			args:    []string{"--name", "-badname"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"--name", "ok", "--bogus", "val"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUpArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name != tt.want.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
			}
			if got.Version != tt.want.Version {
				t.Errorf("Version = %q, want %q", got.Version, tt.want.Version)
			}
		})
	}
}

func TestIsValidName(t *testing.T) {
	valid := []string{"foo", "my-net", "Test123", "a", "abc-def-ghi"}
	for _, name := range valid {
		if !isValidName(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}

	invalid := []string{"", "-starts", "ends-", "has space", "under_score", "a/b", string(make([]byte, 64))}
	for _, name := range invalid {
		if isValidName(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}
