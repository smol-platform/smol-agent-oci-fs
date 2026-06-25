package csinode

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
)

type WorkerManager struct {
	Node         Node
	Reporter     SnapshotReporter
	PollInterval time.Duration

	mu      sync.Mutex
	workers map[string]context.CancelFunc
}

func NewWorkerManager(node Node, reporter SnapshotReporter) *WorkerManager {
	return &WorkerManager{
		Node:         node,
		Reporter:     reporter,
		PollInterval: time.Second,
		workers:      map[string]context.CancelFunc{},
	}
}

func (m *WorkerManager) Run(ctx context.Context) error {
	if m.Reporter == nil {
		m.Reporter = FileReporter{Root: m.Node.reportsDir()}
	}
	if m.PollInterval <= 0 {
		m.PollInterval = time.Second
	}
	if err := m.reconcile(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(m.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return ctx.Err()
		case <-ticker.C:
			if err := m.reconcile(ctx); err != nil {
				log.Printf("osix autosnapshot reconcile: %v", err)
			}
		}
	}
}

func (m *WorkerManager) reconcile(ctx context.Context) error {
	records, err := m.Node.listMountRecords()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, record := range records {
		if record.VolumeID == "" {
			continue
		}
		seen[record.VolumeID] = true
		if !record.AutoSnapshot {
			m.stop(record.VolumeID)
			continue
		}
		m.start(ctx, record)
	}
	m.mu.Lock()
	for volumeID, cancel := range m.workers {
		if !seen[volumeID] {
			cancel()
			delete(m.workers, volumeID)
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *WorkerManager) start(ctx context.Context, record MountRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.workers[record.VolumeID]; exists {
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	m.workers[record.VolumeID] = cancel
	go m.runWorker(workerCtx, record)
}

func (m *WorkerManager) stop(volumeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, exists := m.workers[volumeID]; exists {
		cancel()
		delete(m.workers, volumeID)
	}
}

func (m *WorkerManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for volumeID, cancel := range m.workers {
		cancel()
		delete(m.workers, volumeID)
	}
}

func (m *WorkerManager) runWorker(ctx context.Context, record MountRecord) {
	interval := workerInterval(record.Policy)
	backoff := interval
	if backoff < time.Second {
		backoff = time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		current, err := m.Node.readMountRecord(record.VolumeID)
		if err == nil {
			record = current
		}
		if err != nil || !record.AutoSnapshot {
			m.stop(record.VolumeID)
			return
		}
		request := SnapshotRequest{
			FileSystem: record.FileSystem,
			Policy:     record.Policy,
			VolumeID:   record.VolumeID,
			TargetPath: record.TargetPath,
		}
		decision, snapErr := m.Node.SnapshotNeeded(request)
		if snapErr == nil && !decision.Needed {
			backoff = interval
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
			continue
		}
		var result SnapshotResult
		if snapErr == nil {
			result, snapErr = m.Node.Snapshot(ctx, request)
		}
		if reportErr := m.Reporter.ReportSnapshot(ctx, record, result, snapErr); reportErr != nil {
			log.Printf("osix autosnapshot report volume=%s: %v", record.VolumeID, reportErr)
		}
		sleepFor := interval
		if snapErr != nil {
			log.Printf("osix autosnapshot volume=%s: %v", record.VolumeID, snapErr)
			sleepFor = backoff
			if backoff < 30*time.Second {
				backoff *= 2
			}
		} else {
			backoff = interval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleepFor):
		}
	}
}

func workerInterval(policy *k8soperator.AgentOCISnapshotPolicySpec) time.Duration {
	if policy == nil || policy.Every == "" {
		return 30 * time.Second
	}
	every, err := parseOptionalDuration(policy.Every)
	if err != nil || every <= 0 {
		return 30 * time.Second
	}
	return every
}

func (m *WorkerManager) ActiveWorkers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.workers)
}

func (m *WorkerManager) StartRecord(ctx context.Context, volumeID string) error {
	record, err := m.Node.readMountRecord(volumeID)
	if err != nil {
		return fmt.Errorf("read mount record: %w", err)
	}
	if !record.AutoSnapshot {
		return fmt.Errorf("mount record %s does not enable autosnapshot", volumeID)
	}
	m.start(ctx, record)
	return nil
}
