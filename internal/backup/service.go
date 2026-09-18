package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"controlpanel/internal/audit"
	"github.com/google/uuid"
)

var (
	ErrActiveWork       = errors.New("active work prevents restore")
	ErrChecksumMismatch = errors.New("backup checksum does not match")
)

type Snapshot interface {
	Export(context.Context) (Archive, error)
	Restore(context.Context, Archive) error
}

type ActivityChecker interface {
	HasActiveWork(context.Context) (bool, error)
}
type ActivityCheckFunc func(context.Context) (bool, error)

func (function ActivityCheckFunc) HasActiveWork(ctx context.Context) (bool, error) {
	return function(ctx)
}

type ServiceOptions struct {
	Directory string
	Now       func() time.Time
	NewID     func() string
	Random    io.Reader
}

type Validation struct {
	FormatVersion      int            `json:"format_version"`
	ApplicationVersion string         `json:"application_version"`
	CreatedAt          time.Time      `json:"created_at"`
	Manifest           map[string]int `json:"manifest"`
}

type Service struct {
	repository Repository
	snapshot   Snapshot
	audit      interface {
		Append(context.Context, audit.Entry) error
	}
	activity ActivityChecker
	options  ServiceOptions
	mutex    sync.Mutex
}

func NewService(repository Repository, snapshot Snapshot, auditLog interface {
	Append(context.Context, audit.Entry) error
}, activity ActivityChecker, options ServiceOptions) (*Service, error) {
	if strings.TrimSpace(options.Directory) == "" {
		return nil, errors.New("backup directory is required")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if err := os.MkdirAll(options.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Chmod(options.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure backup directory: %w", err)
	}
	return &Service{repository: repository, snapshot: snapshot, audit: auditLog, activity: activity, options: options}, nil
}

func (service *Service) List(ctx context.Context) ([]Metadata, error) {
	return service.repository.List(ctx)
}

func (service *Service) Create(ctx context.Context, passphrase string, kind Kind, requestID, sourceIP string) (Metadata, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return service.createLocked(ctx, passphrase, kind, requestID, sourceIP)
}

func (service *Service) createLocked(ctx context.Context, passphrase string, kind Kind, requestID, sourceIP string) (Metadata, error) {
	archive, err := service.snapshot.Export(ctx)
	if err != nil {
		return Metadata{}, err
	}
	data, err := Seal(archive, passphrase, service.options.Random)
	if err != nil {
		return Metadata{}, err
	}
	id := service.options.NewID()
	createdAt := service.options.Now().UTC()
	filename := fmt.Sprintf("controlpanel-%s-%s.scpb", createdAt.Format("20060102T150405Z"), id)
	if err := service.writeFile(filename, data); err != nil {
		return Metadata{}, err
	}
	checksum := sha256.Sum256(data)
	manifest, _ := json.Marshal(archive.Manifest)
	metadata := Metadata{ID: id, Filename: filename, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(checksum[:]), FormatVersion: archive.FormatVersion, Kind: kind, Status: StatusReady, Manifest: manifest, CreatedAt: createdAt}
	if err := service.repository.Create(ctx, metadata); err != nil {
		_ = os.Remove(filepath.Join(service.options.Directory, filename))
		return Metadata{}, err
	}
	if err := service.appendAudit(ctx, "backup_created", id, requestID, sourceIP, map[string]any{"kind": kind, "format_version": archive.FormatVersion}); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func (service *Service) Download(ctx context.Context, id string) ([]byte, string, error) {
	metadata, data, err := service.readVerified(ctx, id)
	if err != nil {
		return nil, "", err
	}
	return data, metadata.Filename, nil
}

func (service *Service) Validate(ctx context.Context, id, passphrase string) (Validation, error) {
	_, data, err := service.readVerified(ctx, id)
	if err != nil {
		return Validation{}, err
	}
	archive, err := Open(data, passphrase)
	if err != nil {
		return Validation{}, err
	}
	if err := validateLogicalArchive(archive); err != nil {
		return Validation{}, err
	}
	return Validation{FormatVersion: archive.FormatVersion, ApplicationVersion: archive.ApplicationVersion, CreatedAt: archive.CreatedAt, Manifest: archive.Manifest}, nil
}

func (service *Service) Restore(ctx context.Context, id, passphrase, requestID, sourceIP string) error {
	if service.activity != nil {
		active, err := service.activity.HasActiveWork(ctx)
		if err != nil {
			return err
		}
		if active {
			return ErrActiveWork
		}
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	_, data, err := service.readVerified(ctx, id)
	if err != nil {
		return err
	}
	archive, err := Open(data, passphrase)
	if err != nil {
		return err
	}
	if err := validateLogicalArchive(archive); err != nil {
		return err
	}
	if _, err := service.createLocked(ctx, passphrase, KindSafety, requestID, sourceIP); err != nil {
		return fmt.Errorf("create restore safety backup: %w", err)
	}
	if err := service.snapshot.Restore(ctx, archive); err != nil {
		return err
	}
	return service.appendAudit(ctx, "backup_restored", id, requestID, sourceIP, map[string]any{"format_version": archive.FormatVersion})
}

func (service *Service) readVerified(ctx context.Context, id string) (Metadata, []byte, error) {
	metadata, err := service.repository.FindByID(ctx, id)
	if err != nil {
		return Metadata{}, nil, err
	}
	if filepath.Base(metadata.Filename) != metadata.Filename || !strings.HasSuffix(metadata.Filename, ".scpb") {
		return Metadata{}, nil, ErrInvalidBackup
	}
	path := filepath.Join(service.options.Directory, metadata.Filename)
	file, err := os.Open(path)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 512*1024*1024+1))
	if err != nil || len(data) > 512*1024*1024 {
		return Metadata{}, nil, ErrInvalidBackup
	}
	checksum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(checksum[:]), metadata.SHA256) || int64(len(data)) != metadata.SizeBytes {
		return Metadata{}, nil, ErrChecksumMismatch
	}
	return metadata, data, nil
}

func (service *Service) writeFile(filename string, data []byte) error {
	temporary, err := os.CreateTemp(service.options.Directory, ".controlpanel-backup-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filepath.Join(service.options.Directory, filename))
}

func (service *Service) appendAudit(ctx context.Context, eventType, targetID, requestID, sourceIP string, metadata map[string]any) error {
	if service.audit == nil {
		return nil
	}
	encoded, _ := json.Marshal(metadata)
	return service.audit.Append(ctx, audit.Entry{ID: service.options.NewID(), EventType: eventType, TargetType: "backup", TargetID: targetID, RequestID: requestID, SourceIP: sourceIP, Metadata: encoded, CreatedAt: service.options.Now().UTC()})
}
