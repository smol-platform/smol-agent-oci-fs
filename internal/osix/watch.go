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
	if opts.UntilStopped && opts.Every <= 0 {
		opts.Every = time.Minute
	}
	if opts.Iterations <= 0 && !opts.UntilStopped {
		if opts.Once {
			opts.Iterations = 1
		} else {
			opts.Iterations = 1
		}
	}
	if opts.TagPrefix == "" {
		opts.TagPrefix = "watch"
	}
	ws, err := Workspace(workspaceRoot)
	if err != nil {
		return WatchResult{}, err
	}
	branchTag := ws.DefaultBranch
	if branchTag == "" {
		branchTag = "main"
	}
	statePath, err := watchStatePath(s, target)
	if err != nil {
		return WatchResult{}, err
	}
	result := WatchResult{StatePath: statePath}
	for i := 0; opts.UntilStopped || i < opts.Iterations; i++ {
		if opts.StopPath != "" {
			if _, err := os.Stat(opts.StopPath); err == nil {
				return result, nil
			} else if err != nil && !os.IsNotExist(err) {
				return result, err
			}
		}
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
			snap, err := Snapshot(workspaceRoot, target, SnapshotOptions{Tag: tag, AlsoTag: branchTag, Encrypt: opts.Encrypt, Sign: opts.Sign, Attest: opts.Attest, SecretScan: "warn"})
			if err != nil {
				state.LastError = err.Error()
				_ = writeWatchState(statePath, state)
				return result, err
			}
			state.LastSnapshot = snap.ManifestDigest
			result.Snapshots = append(result.Snapshots, snap)
			if opts.Push {
				err = PushSnapshot(workspaceRoot, ws.StateRef, snap.ManifestDigest, snap.Tags)
				if err != nil {
					state.LastError = err.Error()
					_ = writeWatchState(statePath, state)
					return result, err
				}
			}
			if plan, ran, err := maybeCompactWatchSnapshot(workspaceRoot, ws, branchTag, snap.ManifestDigest, len(result.Snapshots), opts); err != nil {
				state.LastCompaction = &plan
				state.LastError = err.Error()
				_ = writeWatchState(statePath, state)
				return result, err
			} else if ran {
				state.LastCompaction = &plan
				result.Compactions = append(result.Compactions, plan)
			}
		}
		if err := writeWatchState(statePath, state); err != nil {
			return result, err
		}
		if (opts.UntilStopped || i+1 < opts.Iterations) && opts.Every > 0 {
			time.Sleep(opts.Every)
		}
	}
	return result, nil
}

func maybeCompactWatchSnapshot(workspaceRoot string, ws WorkspaceConfig, branchTag, snapshotDigest string, snapshotCount int, opts WatchOptions) (CompactPlan, bool, error) {
	retention := opts.Retention
	if !retention.Enabled() {
		return CompactPlan{}, false, nil
	}
	compactEvery := retention.CompactEvery
	if compactEvery <= 0 {
		compactEvery = 1
	}
	if snapshotCount%compactEvery != 0 {
		return CompactPlan{}, false, nil
	}
	checkpointTag, err := watchCheckpointTag(workspaceRoot, snapshotDigest, retention)
	if err != nil {
		return CompactPlan{}, true, err
	}
	checkpointTags := uniqueTags([]string{checkpointTag, "latest", branchTag})
	plan, err := Compact(workspaceRoot, snapshotDigest, CompactPolicy{
		SquashEvery:    retention.SquashEvery,
		KeepSnapshots:  retention.KeepSnapshots,
		PreserveSigned: retention.PreserveSigned,
		DryRun:         retention.DryRun,
		CheckpointTag:  checkpointTag,
		CheckpointTags: checkpointTags,
		PruneLocal:     retention.PruneLocal,
		PruneSource:    retention.PruneLocal || retention.PruneRemote,
	})
	if err != nil {
		return plan, true, err
	}
	if retention.PruneRemote {
		plan.RemoteDeleteCandidates = append([]string(nil), plan.DeleteCandidates...)
	}
	if !plan.CreateCheckpoint || plan.CheckpointDigest == "" || retention.DryRun {
		return plan, true, nil
	}
	if opts.Push {
		if err := PushSnapshotWithOptions(workspaceRoot, ws.StateRef, plan.CheckpointDigest, plan.CheckpointTags, PushOptions{ExpectedParent: snapshotDigest}); err != nil {
			return plan, true, err
		}
		if retention.PruneRemote && len(plan.DeleteCandidates) > 0 {
			deleted, err := PruneRemoteSnapshots(ws.StateRef, plan.DeleteCandidates)
			if err != nil {
				return plan, true, err
			}
			plan.RemoteDeleted = deleted
		}
	}
	return plan, true, nil
}

func watchCheckpointTag(workspaceRoot, snapshotDigest string, retention WatchRetentionPolicy) (string, error) {
	prefix := retention.CheckpointTagPrefix
	if prefix == "" {
		prefix = "checkpoint"
	}
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", err
	}
	_, _, cfg, err := s.loadManifest(snapshotDigest)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%06d", prefix, cfg.Snapshot.Sequence), nil
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
