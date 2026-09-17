package database

type migration struct {
	Version int
	SQLite  []string
	MySQL   []string
}

var migrations = []migration{
	{
		Version: 1,
		SQLite: []string{
			`CREATE TABLE users (
				singleton_key INTEGER PRIMARY KEY CHECK (singleton_key = 1),
				id TEXT NOT NULL UNIQUE,
				username TEXT NOT NULL,
				normalized_username TEXT NOT NULL UNIQUE,
				password_hash TEXT NOT NULL,
				initialized_at TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				last_login_at TEXT
			)`,
			`CREATE TABLE sessions (
				token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
				csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
				user_id TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				absolute_expires_at TEXT NOT NULL,
				last_seen_at TEXT NOT NULL,
				created_at TEXT NOT NULL,
				FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX sessions_user_id_idx ON sessions(user_id)`,
			`CREATE INDEX sessions_expires_at_idx ON sessions(expires_at)`,
		},
		MySQL: []string{
			`CREATE TABLE users (
				singleton_key TINYINT NOT NULL PRIMARY KEY,
				id VARCHAR(36) NOT NULL UNIQUE,
				username VARCHAR(255) NOT NULL,
				normalized_username VARCHAR(255) NOT NULL UNIQUE,
				password_hash VARCHAR(1024) NOT NULL,
				initialized_at DATETIME(6) NOT NULL,
				created_at DATETIME(6) NOT NULL,
				updated_at DATETIME(6) NOT NULL,
				last_login_at DATETIME(6) NULL,
				CONSTRAINT users_singleton_check CHECK (singleton_key = 1)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
			`CREATE TABLE sessions (
				token_hash BINARY(32) NOT NULL PRIMARY KEY,
				csrf_hash BINARY(32) NOT NULL,
				user_id VARCHAR(36) NOT NULL,
				expires_at DATETIME(6) NOT NULL,
				absolute_expires_at DATETIME(6) NOT NULL,
				last_seen_at DATETIME(6) NOT NULL,
				created_at DATETIME(6) NOT NULL,
				INDEX sessions_user_id_idx (user_id),
				INDEX sessions_expires_at_idx (expires_at),
				CONSTRAINT sessions_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		},
	},
}
