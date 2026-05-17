package config

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultVersion  = "latest"
	cantonImage     = "digitalasset/canton-open-source"
	postgresImage   = "postgres"
	postgresVersion = "14-alpine"
	ledgerAPIPort   = 5011
	adminAPIPort    = 5012
	domainPublicPort = 5018
	domainAdminPort  = 5019
	postgresPort    = 5432
)

type LocalNetConfig struct {
	Name    string
	Version string
	DataDir string
}

type GeneratedIdentity struct {
	PartyName  string
	PublicKey  string
	PrivateKey string
	KeyFile    string
}

func Generate(cfg *LocalNetConfig) error {
	dirs := []string{
		cfg.DataDir,
		filepath.Join(cfg.DataDir, "keys"),
		filepath.Join(cfg.DataDir, "certs"),
		filepath.Join(cfg.DataDir, "canton"),
		filepath.Join(cfg.DataDir, "postgres"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}

	identity, err := generateIdentity(cfg)
	if err != nil {
		return fmt.Errorf("generating identity: %w", err)
	}

	if err := writeCantonConfig(cfg); err != nil {
		return fmt.Errorf("writing canton config: %w", err)
	}

	if err := writeComposeFile(cfg); err != nil {
		return fmt.Errorf("writing compose file: %w", err)
	}

	if err := writeIdentityFile(cfg, identity); err != nil {
		return fmt.Errorf("writing identity file: %w", err)
	}

	return nil
}

func generateIdentity(cfg *LocalNetConfig) (*GeneratedIdentity, error) {
	seed := sha256.Sum256([]byte(cfg.Name + "-participant-admin"))
	reader := &deterministicReader{seed: seed[:], pos: 0}

	pub, priv, err := ed25519.GenerateKey(reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}

	keyFile := filepath.Join(cfg.DataDir, "keys", "participant-admin.key")
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(priv.Seed())), 0o600); err != nil {
		return nil, err
	}

	return &GeneratedIdentity{
		PartyName:  "participant-admin",
		PublicKey:  hex.EncodeToString(pub),
		PrivateKey: hex.EncodeToString(priv.Seed()),
		KeyFile:    keyFile,
	}, nil
}

type deterministicReader struct {
	seed []byte
	pos  int
}

func (d *deterministicReader) Read(p []byte) (int, error) {
	for i := range p {
		if d.pos >= len(d.seed) {
			next := sha256.Sum256(d.seed)
			d.seed = next[:]
			d.pos = 0
		}
		p[i] = d.seed[d.pos]
		d.pos++
	}
	return len(p), nil
}

func sanitizeDBName(name string) string {
	var out []byte
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

func writeCantonConfig(cfg *LocalNetConfig) error {
	dbName := sanitizeDBName(cfg.Name)
	content := fmt.Sprintf(`canton {
  parameters {
    non-standard-config = yes
  }
  domains {
    local {
      storage {
        type = postgres
        config {
          dataSourceClass = "org.postgresql.ds.PGSimpleDataSource"
          properties = {
            serverName = "postgres"
            portNumber = %d
            databaseName = "%s_domain_db"
            user = "canton"
            password = "canton"
          }
        }
      }
      public-api.port = %d
      admin-api.port = %d
    }
  }
  participants {
    participant {
      storage {
        type = postgres
        config {
          dataSourceClass = "org.postgresql.ds.PGSimpleDataSource"
          properties = {
            serverName = "postgres"
            portNumber = %d
            databaseName = "%s_participant_db"
            user = "canton"
            password = "canton"
          }
        }
      }
      ledger-api.port = %d
      admin-api.port = %d
    }
  }
}
`, postgresPort, dbName, domainPublicPort, domainAdminPort, postgresPort, dbName, ledgerAPIPort, adminAPIPort)

	return os.WriteFile(filepath.Join(cfg.DataDir, "canton", "canton.conf"), []byte(content), 0o644)
}

func writeComposeFile(cfg *LocalNetConfig) error {
	content := fmt.Sprintf(`services:
  postgres:
    image: %s:%s
    environment:
      POSTGRES_USER: canton
      POSTGRES_PASSWORD: canton
    ports:
      - "%d:%d"
    volumes:
      - ./postgres:/var/lib/postgresql/data
      - ./init-db.sql:/docker-entrypoint-initdb.d/init.sql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U canton"]
      interval: 5s
      timeout: 3s
      retries: 10

  canton:
    image: %s:%s
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "%d:%d"
      - "%d:%d"
    volumes:
      - ./canton:/etc/canton
      - ./keys:/etc/canton/keys
    command: ["daemon", "--config", "/etc/canton/canton.conf", "--auto-connect-local"]
    healthcheck:
      test: ["CMD-SHELL", "bash -c '</dev/tcp/localhost/%d' || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 30
      start_period: 40s
`,
		postgresImage, postgresVersion,
		postgresPort, postgresPort,
		cantonImage, cfg.Version,
		ledgerAPIPort, ledgerAPIPort,
		adminAPIPort, adminAPIPort,
		ledgerAPIPort,
	)

	if err := os.WriteFile(filepath.Join(cfg.DataDir, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		return err
	}

	dbSafe := sanitizeDBName(cfg.Name)
	initSQL := fmt.Sprintf("CREATE DATABASE %s_domain_db;\nCREATE DATABASE %s_participant_db;\n", dbSafe, dbSafe)

	return os.WriteFile(filepath.Join(cfg.DataDir, "init-db.sql"), []byte(initSQL), 0o644)
}

func writeIdentityFile(cfg *LocalNetConfig, id *GeneratedIdentity) error {
	content := fmt.Sprintf(`# Canton LocalNet Identity: %s
party_name: %s
public_key: %s
key_file: %s
`, cfg.Name, id.PartyName, id.PublicKey, id.KeyFile)

	return os.WriteFile(filepath.Join(cfg.DataDir, "identity.txt"), []byte(content), 0o644)
}

func DataDir(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".canton", "localnet", name)
}
