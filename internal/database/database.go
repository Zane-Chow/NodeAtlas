package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"controlpanel/internal/config"
	"github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

type Dialect string

const (
	DialectSQLite Dialect = "sqlite"
	DialectMySQL  Dialect = "mysql"
)

func Open(ctx context.Context, cfg config.DatabaseConfig) (*sql.DB, Dialect, error) {
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, "", errors.New("parse database URL")
	}

	var driver, dsn string
	var dialect Dialect
	switch parsed.Scheme {
	case "sqlite":
		dialect = DialectSQLite
		driver = "sqlite"
		dsn, err = sqliteDSN(parsed)
	case "mysql":
		dialect = DialectMySQL
		driver = "mysql"
		dsn, err = mysqlDSN(parsed)
	default:
		err = errors.New("unsupported database scheme")
	}
	if err != nil {
		return nil, "", err
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, "", fmt.Errorf("open database: %w", err)
	}
	if dialect == DialectSQLite {
		db.SetMaxOpenConns(1)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, "", fmt.Errorf("connect database: %w", err)
	}
	return db, dialect, nil
}

func sqliteDSN(parsed *url.URL) (string, error) {
	databasePath := parsed.Path
	if parsed.Host != "" {
		databasePath = filepath.Join(parsed.Host, parsed.Path)
	}
	if databasePath == "" {
		return "", errors.New("sqlite database path is empty")
	}
	if databasePath != ":memory:" {
		directory := filepath.Dir(databasePath)
		if directory != "." {
			if err := ensureDirectory(directory); err != nil {
				return "", err
			}
		}
	}
	return "file:" + databasePath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", nil
}

var ensureDirectory = func(path string) error {
	return makeDirectory(path)
}

func mysqlDSN(parsed *url.URL) (string, error) {
	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if parsed.Host == "" || databaseName == "" {
		return "", errors.New("mysql URL requires host and database")
	}
	configuration := mysql.NewConfig()
	configuration.User = username
	configuration.Passwd = password
	configuration.Net = "tcp"
	configuration.Addr = parsed.Host
	configuration.DBName = databaseName
	configuration.ParseTime = true
	configuration.Params = map[string]string{"charset": "utf8mb4"}
	return configuration.FormatDSN(), nil
}
