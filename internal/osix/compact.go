package osix

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Compact(workspaceRoot, ref string, policy CompactPolicy) (CompactPlan, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return CompactPlan{}, err
	}
	digest, err := s.resolveRef(ref)
	if err != nil {
		return CompactPlan{}, err
	}
	chain, err := s.snapshotChainWithDigests(digest)
	if err != nil {
		return CompactPlan{}, err
	}
	if policy.SquashEvery <= 0 {
		policy.SquashEvery = 50
	}
	keepSet := map[string]bool{}
	for _, keep := range policy.KeepSnapshots {
		if keep == "" {
			continue
		}
		if resolved, err := s.resolveRef(keep); err == nil {
			keepSet[resolved] = true
		}
		keepSet[keep] = true
	}
	plan := CompactPlan{
		SourceRef:        ref,
		SourceDigest:     digest,
		ChainLength:      len(chain),
		CreateCheckpoint: len(chain) >= policy.SquashEvery,
		CheckpointTag:    policy.CheckpointTag,
	}
	if plan.CheckpointTag == "" {
		plan.CheckpointTag = fmt.Sprintf("checkpoint-%06d", chain[len(chain)-1].Config.Snapshot.Sequence)
	}
	for i, item := range chain {
		reason := ""
		if i == len(chain)-1 {
			reason = "branch head"
		} else if keepSet[item.Digest] || keepSet[item.Config.Snapshot.ID] {
			reason = "explicit keep"
		} else if policy.PreserveSigned && s.hasSignature(item.Digest) {
			reason = "signed snapshot"
		}
		if reason != "" {
			plan.Keep = append(plan.Keep, item.Digest)
			plan.Reasons = append(plan.Reasons, item.Digest+": "+reason)
		} else {
			plan.DeleteCandidates = append(plan.DeleteCandidates, item.Digest)
		}
	}
	if policy.DryRun || !plan.CreateCheckpoint {
		if !plan.CreateCheckpoint {
			plan.Reasons = append(plan.Reasons, "chain shorter than squash threshold")
		}
		return plan, nil
	}
	tmp, err := os.MkdirTemp("", "osix-compact-*")
	if err != nil {
		return plan, err
	}
	defer os.RemoveAll(tmp)
	if err := Restore(workspaceRoot, digest, tmp, RestoreOptions{Force: true}); err != nil {
		return plan, err
	}
	snap, err := Snapshot(workspaceRoot, tmp, SnapshotOptions{
		Tag:        plan.CheckpointTag,
		AlsoTag:    "latest",
		Message:    "compacted checkpoint from " + ref,
		Checkpoint: true,
		SecretScan: "warn",
	})
	if err != nil {
		return plan, err
	}
	plan.CheckpointDigest = snap.ManifestDigest
	return plan, nil
}

func (s store) hasSignature(digest string) bool {
	_, err := s.resolveRef(signatureRefName(digest))
	return err == nil
}

func ParseCompactPolicy(squashEvery int, keepSnapshots string, preserveSigned bool, dryRun bool, checkpointTag string) CompactPolicy {
	var keep []string
	for _, item := range strings.Split(keepSnapshots, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			keep = append(keep, item)
		}
	}
	return CompactPolicy{
		SquashEvery:    squashEvery,
		KeepSnapshots:  keep,
		PreserveSigned: preserveSigned,
		DryRun:         dryRun,
		CheckpointTag:  checkpointTag,
	}
}

func releaseArtifactPath(root, name string) string {
	return filepath.Join(root, "dist", name)
}
