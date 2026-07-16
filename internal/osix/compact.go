package osix

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		CheckpointTags:   uniqueTags(policy.CheckpointTags),
	}
	if plan.CheckpointTag == "" {
		plan.CheckpointTag = fmt.Sprintf("checkpoint-%06d", chain[len(chain)-1].Config.Snapshot.Sequence)
	}
	plan.CheckpointTags = uniqueTags(append([]string{plan.CheckpointTag}, plan.CheckpointTags...))
	for i, item := range chain {
		reason := ""
		if keepSet[item.Digest] || keepSet[item.Config.Snapshot.ID] {
			reason = "explicit keep"
		} else if policy.PreserveSigned && s.hasSignature(item.Digest) {
			reason = "signed snapshot"
		} else if i == len(chain)-1 && !policy.PruneSource {
			reason = "branch head"
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
	snapshotTarget := policy.SourceTarget
	if snapshotTarget == "" {
		tmp, err := os.MkdirTemp("", "osix-compact-*")
		if err != nil {
			return plan, err
		}
		defer os.RemoveAll(tmp)
		if err := Restore(workspaceRoot, digest, tmp, RestoreOptions{Force: true, Decrypt: policy.Decrypt}); err != nil {
			return plan, err
		}
		snapshotTarget = tmp
	} else {
		tree, _, err := scanTree(snapshotTarget)
		if err != nil {
			return plan, err
		}
		tree, err = redactedSnapshotTree(snapshotTarget, tree)
		if err != nil {
			return plan, err
		}
		want := chain[len(chain)-1].Config.Integrity.MTreeDigest
		if want == "" {
			want = digestTree(chain[len(chain)-1].Config.Tree)
		}
		if got := digestTree(tree); got != want {
			return plan, fmt.Errorf("checkpoint source changed after snapshot: got tree %s want %s", got, want)
		}
	}
	snap, err := Snapshot(workspaceRoot, snapshotTarget, SnapshotOptions{
		Tag:        plan.CheckpointTag,
		AlsoTag:    "latest",
		Message:    "compacted checkpoint from " + ref,
		Checkpoint: true,
		SecretScan: "warn",
		Encrypt:    policy.Encrypt,
	})
	if err != nil {
		return plan, err
	}
	plan.CheckpointDigest = snap.ManifestDigest
	for _, tag := range plan.CheckpointTags {
		if err := s.writeRef(tag, snap.ManifestDigest); err != nil {
			return plan, err
		}
	}
	if policy.PruneLocal {
		prunedRefs, prunedBlobs, err := s.pruneLocalSnapshots(plan.DeleteCandidates)
		if err != nil {
			return plan, err
		}
		plan.PrunedRefs = prunedRefs
		plan.PrunedBlobs = prunedBlobs
	}
	return plan, nil
}

func (s store) pruneLocalSnapshots(candidates []string) ([]string, []string, error) {
	deleteSet := map[string]bool{}
	candidateBlobs := map[string]bool{}
	for _, digest := range candidates {
		digest = strings.TrimSpace(digest)
		if digest == "" {
			continue
		}
		deleteSet[digest] = true
		candidateBlobs[digest] = true
		if _, manifest, _, err := s.loadManifest(digest); err == nil {
			candidateBlobs[manifest.Config.Digest] = true
			for _, layer := range manifest.Layers {
				candidateBlobs[layer.Digest] = true
			}
		}
	}
	if len(deleteSet) == 0 {
		return nil, nil, nil
	}

	var prunedRefs []string
	refs, err := s.listRefs()
	if err != nil {
		return nil, nil, err
	}
	for _, ref := range refs {
		if !deleteSet[ref.Digest] {
			continue
		}
		path := filepath.Join(s.refsRoot(), safeRefName(ref.Name))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return prunedRefs, nil, err
		}
		prunedRefs = append(prunedRefs, ref.Name)
	}
	sort.Strings(prunedRefs)

	reachable, err := s.reachableBlobDigests()
	if err != nil {
		return prunedRefs, nil, err
	}
	var prunedBlobs []string
	for digest := range candidateBlobs {
		if reachable[digest] {
			continue
		}
		hexDigest, err := digestHex(digest)
		if err != nil {
			return prunedRefs, prunedBlobs, err
		}
		path := filepath.Join(s.blobRoot(), hexDigest)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return prunedRefs, prunedBlobs, err
		}
		prunedBlobs = append(prunedBlobs, digest)
	}
	sort.Strings(prunedBlobs)
	return prunedRefs, prunedBlobs, nil
}

func (s store) reachableBlobDigests() (map[string]bool, error) {
	reachable := map[string]bool{}
	refs, err := s.listRefs()
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		reachable[ref.Digest] = true
		chain, err := s.snapshotChainWithDigests(ref.Digest)
		if err != nil {
			continue
		}
		for _, item := range chain {
			reachable[item.Digest] = true
			reachable[item.Manifest.Config.Digest] = true
			for _, layer := range item.Manifest.Layers {
				reachable[layer.Digest] = true
			}
		}
	}
	return reachable, nil
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
