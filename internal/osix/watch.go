package osix

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func Watch(workspaceRoot, target string, opts WatchOptions) (WatchResult, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return WatchResult{}, err
	}
	if opts.Iterations <= 0 {
		if opts.Once {
			opts.Iterations = 1
		} else {
			opts.Iterations = 1
		}
	}
	if opts.TagPrefix == "" {
		opts.TagPrefix = "watch"
	}
	statePath, err := watchStatePath(s, target)
	if err != nil {
		return WatchResult{}, err
	}
	result := WatchResult{StatePath: statePath}
	for i := 0; i < opts.Iterations; i++ {
		state := WatchState{Target: absPath(target), Iterations: i + 1, UpdatedAt: time.Now().UTC().Truncate(time.Second)}
		if opts.OnTurnBoundary {
			if err := waitTurnBoundary(target, opts.Every); err != nil {
				state.LastError = err.Error()
				_ = writeWatchState(statePath, state)
				return result, err
			}
		}
		dirtyBytes, err := dirtyBytesForTarget(workspaceRoot, target)
		if err != nil {
			state.LastError = err.Error()
			_ = writeWatchState(statePath, state)
			return result, err
		}
		shouldSnapshot := opts.MaxDirtyBytes <= 0 || dirtyBytes >= opts.MaxDirtyBytes || opts.Every > 0 || opts.Once
		if shouldSnapshot {
			if err := flushRuntimeForTarget(workspaceRoot, target); err != nil {
				state.LastError = err.Error()
				_ = writeWatchState(statePath, state)
				return result, err
			}
			tag := fmt.Sprintf("%s-%06d", opts.TagPrefix, time.Now().UTC().Unix())
			snap, err := Snapshot(workspaceRoot, target, SnapshotOptions{Tag: tag, AlsoTag: "latest", Encrypt: opts.Encrypt, SecretScan: "warn"})
			if err != nil {
				state.LastError = err.Error()
				_ = writeWatchState(statePath, state)
				return result, err
			}
			state.LastSnapshot = snap.ManifestDigest
			result.Snapshots = append(result.Snapshots, snap)
			if opts.Push {
				ws, err := Workspace(workspaceRoot)
				if err == nil {
					err = PushSnapshot(workspaceRoot, ws.StateRef, snap.ManifestDigest, snap.Tags)
				}
				if err != nil {
					state.LastError = err.Error()
					_ = writeWatchState(statePath, state)
					return result, err
				}
			}
		}
		if err := writeWatchState(statePath, state); err != nil {
			return result, err
		}
		if i+1 < opts.Iterations && opts.Every > 0 {
			time.Sleep(opts.Every)
		}
	}
	return result, nil
}

func watchStatePath(s store, target string) (string, error) {
	key, err := mountKey(target)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.root, "watch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, key+".json"), nil
}

func writeWatchState(path string, state WatchState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

func waitTurnBoundary(target string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		for _, candidate := range []string{
			filepath.Join(target, ".osix-turn-boundary"),
			filepath.Join(target, "agent", "state", "turn-boundary"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for turn boundary")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
