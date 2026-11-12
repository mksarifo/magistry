package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"magistry/internal/auth"
)

type Usage struct {
	UsedBytes       int64  `json:"usedBytes"`
	BlobBytes       int64  `json:"blobBytes"`
	UploadBytes     int64  `json:"uploadBytes"`
	MetadataBytes   int64  `json:"metadataBytes"`
	Blobs           int64  `json:"blobs"`
	FilesystemBytes uint64 `json:"filesystemBytes"`
	AvailableBytes  uint64 `json:"availableBytes"`
	LimitBytes      int64  `json:"limitBytes"`
}
type settings struct {
	LimitBytes int64 `json:"limitBytes"`
}
type Job struct {
	State          string `json:"state"`
	DryRun         bool   `json:"dryRun"`
	Started        string `json:"started,omitempty"`
	Completed      string `json:"completed,omitempty"`
	Output         string `json:"output,omitempty"`
	Error          string `json:"error,omitempty"`
	ReclaimedBytes int64  `json:"reclaimedBytes"`
}
type Manager struct {
	Gate                       sync.RWMutex
	mu                         sync.Mutex
	root, file, binary, config string
	settings                   settings
	job                        Job
}

func New(root, file, binary, config string) (*Manager, error) {
	m := &Manager{root: root, file: file, binary: binary, config: config, job: Job{State: "idle"}}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if err := json.Unmarshal(data, &m.settings); err != nil || m.settings.LimitBytes < 0 {
				return nil, errors.New("Invalid storage settings")
			}
		}
	}
	return m, nil
}

func (m *Manager) Usage(ctx context.Context) (Usage, error) {
	m.mu.Lock()
	limit := m.settings.LimitBytes
	m.mu.Unlock()
	u := Usage{LimitBytes: limit}
	if m.root == "" {
		return u, errors.New("Storage management is unavailable: configure REGISTRY_DATA_DIR")
	}
	err := filepath.WalkDir(m.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		size := info.Size()
		u.UsedBytes += size
		rel, _ := filepath.Rel(m.root, path)
		rel = filepath.ToSlash(rel)
		switch {
		case strings.Contains(rel, "/blobs/") && d.Name() == "data":
			u.BlobBytes += size
			u.Blobs++
		case strings.Contains(rel, "/_uploads/"):
			u.UploadBytes += size
		default:
			u.MetadataBytes += size
		}
		return nil
	})
	if err != nil {
		return u, err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.root, &stat); err != nil {
		return u, err
	}
	u.FilesystemBytes = stat.Blocks * uint64(stat.Bsize)
	u.AvailableBytes = stat.Bavail * uint64(stat.Bsize)
	return u, nil
}

func (m *Manager) SetLimit(limit int64) error {
	if limit < 0 || limit > 1<<60 {
		return errors.New("Storage limit must be between 0 and 1 EiB")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := settings{LimitBytes: limit}
	if err := auth.SaveJSON(m.file, s); err != nil {
		return err
	}
	m.settings = s
	return nil
}

func (m *Manager) CheckUpload(ctx context.Context, length int64) error {
	m.mu.Lock()
	limit := m.settings.LimitBytes
	m.mu.Unlock()
	if limit == 0 {
		return nil
	}
	u, err := m.Usage(ctx)
	if err != nil {
		return err
	}
	if u.UsedBytes >= limit || (length > 0 && length > limit-u.UsedBytes) {
		return errors.New("Registry storage limit reached. Free unused data or increase the limit.")
	}
	return nil
}

func (m *Manager) Job() Job { m.mu.Lock(); defer m.mu.Unlock(); return m.job }

type limitedOutput struct {
	mu   sync.Mutex
	data []byte
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if remaining := 65536 - len(w.data); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}

func (m *Manager) StartCleanup(dryRun bool) error {
	if m.root == "" || m.binary == "" || m.config == "" {
		return errors.New("Cleanup is unavailable in this deployment")
	}
	if _, err := exec.LookPath(m.binary); err != nil {
		return errors.New("Registry garbage collector is unavailable")
	}
	m.mu.Lock()
	if m.job.State == "running" {
		m.mu.Unlock()
		return errors.New("A storage job is already running")
	}
	m.job = Job{State: "running", DryRun: dryRun, Started: time.Now().UTC().Format(time.RFC3339)}
	m.mu.Unlock()
	go func() {
		// The upstream registry is private. Hold every proxied request and API
		// deletion out of the volume for the complete mark-and-sweep operation.
		m.Gate.Lock()
		defer m.Gate.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		before, err := m.Usage(ctx)
		var output limitedOutput
		if err == nil {
			args := []string{"garbage-collect"}
			if dryRun {
				args = append(args, "--dry-run")
			}
			args = append(args, m.config)
			cmd := exec.CommandContext(ctx, m.binary, args...)
			cmd.Stdout = &output
			cmd.Stderr = &output
			err = cmd.Run()
		}
		after, scanErr := m.Usage(ctx)
		if err == nil {
			err = scanErr
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.job.State = "done"
		m.job.Completed = time.Now().UTC().Format(time.RFC3339)
		m.job.Output = string(output.data)
		if !dryRun && err == nil && before.UsedBytes > after.UsedBytes {
			m.job.ReclaimedBytes = before.UsedBytes - after.UsedBytes
		}
		if err != nil {
			m.job.State = "error"
			m.job.Error = err.Error()
		}
	}()
	return nil
}
