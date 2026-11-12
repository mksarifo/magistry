package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageCountsUniqueBlobsUploadsAndMetadata(t *testing.T) {
	root := t.TempDir()
	for path, size := range map[string]int{"docker/registry/v2/blobs/sha256/ab/abcdef/data": 100, "docker/registry/v2/repositories/app/_uploads/id/data": 25, "docker/registry/v2/repositories/app/_layers/sha256/abcdef/link": 10} {
		p := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := New(root, filepath.Join(t.TempDir(), "storage.json"), "", "")
	u, err := m.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.UsedBytes != 135 || u.BlobBytes != 100 || u.UploadBytes != 25 || u.MetadataBytes != 10 || u.Blobs != 1 {
		t.Fatalf("wrong usage %+v", u)
	}
	if err := m.SetLimit(140); err != nil {
		t.Fatal(err)
	}
	if m.CheckUpload(context.Background(), 6) == nil || m.CheckUpload(context.Background(), 5) != nil {
		t.Fatal("upload limit not enforced")
	}
	reloaded, err := New(root, m.file, "", "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ = reloaded.Usage(context.Background())
	if u.LimitBytes != 140 {
		t.Fatal("limit not persisted")
	}
	if m.SetLimit(-1) == nil {
		t.Fatal("negative limit accepted")
	}
	m.SetLimit(0)
	if m.CheckUpload(context.Background(), 9999999) != nil {
		t.Fatal("unlimited uploads rejected")
	}
}

func TestFailedCollectorReleasesMaintenanceGate(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(t.TempDir(), "collector")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho collector-failed\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m, _ := New(root, "", binary, "config.yml")
	if err := m.StartCleanup(true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Job().State == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	j := m.Job()
	if j.State != "error" || j.Error == "" || j.Output == "" {
		t.Fatalf("collector error lost %+v", j)
	}
	if !m.Gate.TryRLock() {
		t.Fatal("failed collector left registry blocked")
	}
	m.Gate.RUnlock()
}
