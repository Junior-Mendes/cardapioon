package db

import (
	"embed"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration representa um ficheiro SQL versionado em internal/db/migrations.
type migration struct {
	Version int
	Name    string
	SQL     string
}

// RunMigrations aplica, por ordem de versão, as migrações ainda não registadas em
// schema_migrations. Substitui o AutoMigrate: este último não gera renomeações nem
// remoções de colunas, o que o torna inadequado para uma base com dados reais.
//
// Cada migração corre dentro da sua própria transação. O MySQL faz commit implícito
// em DDL, pelo que a transação não garante atomicidade de um ALTER TABLE; garante,
// isso sim, que o registo em schema_migrations só existe se os statements passarem.
// Migrações devem por isso ser escritas de forma idempotente sempre que possível.
func RunMigrations(gdb *gorm.DB) error {
	if err := ensureMigrationsTable(gdb); err != nil {
		return fmt.Errorf("criar tabela schema_migrations: %w", err)
	}

	applied, err := appliedVersions(gdb)
	if err != nil {
		return fmt.Errorf("ler migrações aplicadas: %w", err)
	}

	all, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("carregar migrações: %w", err)
	}

	pending := 0
	for _, m := range all {
		if applied[m.Version] {
			continue
		}
		pending++
		slog.Info("aplicando migração", "version", m.Version, "name", m.Name)

		if err := applyMigration(gdb, m); err != nil {
			return fmt.Errorf("migração %04d_%s: %w", m.Version, m.Name, err)
		}
	}

	if pending == 0 {
		slog.Info("schema já actualizado", "versoes_aplicadas", len(applied))
	} else {
		slog.Info("migrações aplicadas com sucesso", "aplicadas_agora", pending)
	}
	return nil
}

func ensureMigrationsTable(gdb *gorm.DB) error {
	return gdb.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INT          NOT NULL PRIMARY KEY,
		name       VARCHAR(191) NOT NULL,
		applied_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`).Error
}

func appliedVersions(gdb *gorm.DB) (map[int]bool, error) {
	var versions []int
	if err := gdb.Raw("SELECT version FROM schema_migrations").Scan(&versions).Error; err != nil {
		return nil, err
	}
	set := make(map[int]bool, len(versions))
	for _, v := range versions {
		set[v] = true
	}
	return set, nil
}

func applyMigration(gdb *gorm.DB, m migration) error {
	return gdb.Transaction(func(tx *gorm.DB) error {
		for _, stmt := range splitStatements(m.SQL) {
			if err := tx.Exec(stmt).Error; err != nil {
				return fmt.Errorf("statement %q: %w", truncate(stmt, 120), err)
			}
		}
		return tx.Exec(
			"INSERT INTO schema_migrations (version, name) VALUES (?, ?)",
			m.Version, m.Name,
		).Error
	})
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	var out []migration
	seen := map[int]string{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}

		// Formato esperado: 0001_nome_descritivo.sql
		base := strings.TrimSuffix(e.Name(), ".sql")
		parts := strings.SplitN(base, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("nome de migração inválido %q: esperado <versao>_<nome>.sql", e.Name())
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("versão inválida em %q: %w", e.Name(), err)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("versão %d duplicada: %q e %q", version, prev, e.Name())
		}
		seen[version] = e.Name()

		content, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}

		out = append(out, migration{Version: version, Name: parts[1], SQL: string(content)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// splitStatements separa os statements de um ficheiro por ';', ignorando comentários
// de linha e respeitando literais entre aspas simples/duplas e crases, para que um ';'
// dentro de uma string não parta o statement.
func splitStatements(sql string) []string {
	var (
		out     []string
		current strings.Builder
		quote   rune // 0 = fora de literal
	)

	lines := strings.Split(sql, "\n")
	for _, line := range lines {
		if quote == 0 {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}
		}

		for _, r := range line {
			switch {
			case quote != 0:
				if r == quote {
					quote = 0
				}
			case r == '\'' || r == '"' || r == '`':
				quote = r
			case r == ';':
				if stmt := strings.TrimSpace(current.String()); stmt != "" {
					out = append(out, stmt)
				}
				current.Reset()
				continue
			}
			current.WriteRune(r)
		}
		current.WriteRune('\n')
	}

	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
