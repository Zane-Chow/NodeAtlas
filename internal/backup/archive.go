package backup

import (
	"errors"
	"time"

	"controlpanel/internal/database"
)

const CurrentFormatVersion = 1

var (
	ErrInvalidBackup   = errors.New("backup is invalid or passphrase is incorrect")
	ErrInvalidManifest = errors.New("backup manifest is invalid")
)

type Archive struct {
	FormatVersion      int                  `json:"format_version"`
	ApplicationVersion string               `json:"application_version"`
	CreatedAt          time.Time            `json:"created_at"`
	SourceDialect      database.Dialect     `json:"source_dialect"`
	Manifest           map[string]int       `json:"manifest"`
	Tables             map[string]TableData `json:"tables"`
}

type TableData struct {
	Columns []string    `json:"columns"`
	Rows    [][]*string `json:"rows"`
}

func (archive Archive) validateEnvelopeManifest() error {
	if archive.FormatVersion != CurrentFormatVersion || archive.CreatedAt.IsZero() || archive.Manifest == nil || archive.Tables == nil {
		return ErrInvalidManifest
	}
	if archive.SourceDialect != database.DialectSQLite && archive.SourceDialect != database.DialectMySQL {
		return ErrInvalidManifest
	}
	if len(archive.Manifest) != len(archive.Tables) {
		return ErrInvalidManifest
	}
	for name, count := range archive.Manifest {
		table, ok := archive.Tables[name]
		if !ok || count != len(table.Rows) || count < 0 || len(table.Columns) == 0 {
			return ErrInvalidManifest
		}
		for _, row := range table.Rows {
			if len(row) != len(table.Columns) {
				return ErrInvalidManifest
			}
		}
	}
	return nil
}
