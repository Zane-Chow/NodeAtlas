package database

import "os"

func makeDirectory(path string) error {
	return os.MkdirAll(path, 0o700)
}
