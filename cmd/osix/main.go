package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

const usage = `osix is a local prototype for OCI Agent State Images.

Usage:
  osix init BASE --name NAME --state REF --mount DIR [--encrypt RECIPIENTS]
  osix snapshot DIR [--message MSG] [--tag TAG] [--also-tag TAG] [--expected-parent DIGEST] [--encrypt RECIPIENTS] [--sign KEY|keyless] [--attest TYPE]
  osix push REF [REGISTRY/REPO] [--tag TAG]
  osix pull REGISTRY/REPO:TAG [--tag LOCAL_TAG]
  osix restore REF DIR [--force] [--decrypt IDENTITIES]
  osix mount REF DIR [--mode auto|overlay|fuse|materialized] [--rw] [--branch BRANCH] [--force] [--decrypt IDENTITIES] [--cache DIR] [--lazy]
  osix mount status DIR
  osix mount recover DIR
  osix unmount DIR [--force]
  osix diff REF_A REF_B
  osix diff MOUNT_DIR
  osix fork SOURCE_REF TARGET_TAG
  osix verify REF [--trusted-key PUBKEY]
  osix validate REF
  osix watch DIR [--once] [--every DURATION] [--max-dirty BYTES] [--on-turn-boundary] [--push]
  osix compact REF [--dry-run] [--squash-every N] [--keep-snapshots A,B] [--preserve-signed]
  osix run MOUNT_DIR -- COMMAND [ARG...]
  osix show REF
  osix refs

Refs are local tags, immutable sha256:... manifest digests, or remote OCI refs
such as localhost:5000/acme/agent-state:snap-000001.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "osix: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "push":
		return runPush(args[1:])
	case "pull":
		return runPull(args[1:])
	case "restore":
		return runRestore(args[1:], false)
	case "mount":
		return runMount(args[1:])
	case "unmount":
		return runUnmount(args[1:])
	case "diff":
		return runDiff(args[1:])
	case "fork":
		return runFork(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "validate":
		return runValidate(args[1:])
	case "watch":
		return runWatch(args[1:])
	case "compact":
		return runCompact(args[1:])
	case "run":
		return runCommand(args[1:])
	case "show":
		return runShow(args[1:])
	case "refs":
		return runRefs(args[1:])
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

type repeatedStrings []string

func (v *repeatedStrings) String() string {
	return strings.Join(*v, ",")
}

func (v *repeatedStrings) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty tag")
	}
	*v = append(*v, value)
	return nil
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	name := fs.String("name", "", "agent name")
	state := fs.String("state", "local/osix-state", "state image reference")
	mount := fs.String("mount", "./agentfs", "local writable mount directory")
	branch := fs.String("branch", "main", "default branch")
	encrypt := fs.String("encrypt", "", "default encryption recipients, comma-separated")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix init BASE --name NAME --state REF --mount DIR")
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	cfg, err := osix.Init(".", osix.InitOptions{
		Base:          fs.Arg(0),
		Name:          *name,
		StateRef:      *state,
		Mount:         *mount,
		DefaultBranch: *branch,
		Encrypt:       *encrypt,
	})
	if err != nil {
		return err
	}
	fmt.Printf("initialized OSIx workspace\n")
	fmt.Printf("  agent:  %s\n", cfg.Name)
	fmt.Printf("  base:   %s\n", cfg.Base)
	fmt.Printf("  state:  %s\n", cfg.StateRef)
	fmt.Printf("  mount:  %s\n", cfg.Mount)
	return nil
}

func runPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	var tags repeatedStrings
	fs.Var(&tags, "tag", "remote tag to publish for the selected snapshot; may be repeated")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("usage: osix push REF [REGISTRY/REPO] [--tag TAG]")
	}
	ref := fs.Arg(0)
	remoteRepo := ""
	if fs.NArg() == 2 {
		remoteRepo = fs.Arg(1)
	} else {
		cfg, err := osix.Workspace(".")
		if err != nil {
			return err
		}
		remoteRepo = cfg.StateRef
	}
	if !strings.HasPrefix(ref, "sha256:") && !containsString(tags, ref) {
		tags = append(tags, ref)
	}
	if err := osix.PushSnapshot(".", remoteRepo, ref, tags); err != nil {
		return err
	}
	fmt.Printf("pushed %s to %s\n", ref, remoteRepo)
	if len(tags) > 0 {
		fmt.Printf("tags %s\n", strings.Join(tags, ","))
	}
	return nil
}

func runPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	localTag := fs.String("tag", "", "local tag to write for the pulled snapshot")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix pull REGISTRY/REPO:TAG [--tag LOCAL_TAG]")
	}
	remoteRef := fs.Arg(0)
	if *localTag == "" {
		parsed, err := osix.ParseRegistryReference(remoteRef)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(parsed.Reference, "sha256:") {
			*localTag = parsed.Reference
		}
	}
	digest, err := osix.PullSnapshot(".", remoteRef, *localTag)
	if err != nil {
		return err
	}
	fmt.Printf("pulled %s\n", digest)
	if *localTag != "" {
		fmt.Printf("tagged %s -> %s\n", *localTag, digest)
	}
	return nil
}

func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	msg := fs.String("message", "", "snapshot message")
	tag := fs.String("tag", "", "tag to write")
	alsoTag := fs.String("also-tag", "", "additional tag to write")
	encrypt := fs.String("encrypt", "", "encryption recipients, comma-separated")
	sign := fs.String("sign", "", "sign manifest digest with key path or keyless")
	attest := fs.String("attest", "", "provenance attestation label")
	expectedParent := fs.String("expected-parent", "", "expected current digest for mutable tag updates")
	secretScan := fs.String("secret-scan", "warn", "secret scan mode: block, warn, or off")
	push := fs.Bool("push", false, "push snapshot to workspace state registry repo")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"push": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix snapshot DIR [--message MSG] [--tag TAG] [--also-tag TAG]")
	}
	result, err := osix.Snapshot(".", fs.Arg(0), osix.SnapshotOptions{
		Message:        *msg,
		Tag:            *tag,
		AlsoTag:        *alsoTag,
		Encrypt:        *encrypt,
		Sign:           *sign,
		Attest:         *attest,
		ExpectedParent: *expectedParent,
		SecretScan:     *secretScan,
	})
	if err != nil {
		return err
	}
	if *push {
		cfg, err := osix.Workspace(".")
		if err != nil {
			return err
		}
		if err := osix.PushSnapshot(".", cfg.StateRef, result.ManifestDigest, result.Tags); err != nil {
			return err
		}
	}
	fmt.Printf("%s\n", result.ManifestDigest)
	for _, tag := range result.Tags {
		fmt.Printf("tagged %s -> %s\n", tag, result.ManifestDigest)
	}
	if *push {
		cfg, err := osix.Workspace(".")
		if err != nil {
			return err
		}
		fmt.Printf("pushed %s with tags %s\n", cfg.StateRef, strings.Join(result.Tags, ","))
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func runRestore(args []string, mountAlias bool) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing non-empty target")
	decrypt := fs.String("decrypt", "", "decrypt identities or KMS recipients, comma-separated")
	trustedKey := fs.String("trusted-key", "", "verify snapshot with trusted key before restore")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"force": true}); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: osix restore REF DIR [--force]")
	}
	ref := fs.Arg(0)
	if osix.IsRegistryReference(ref) {
		digest, err := osix.PullSnapshot(".", ref, "")
		if err != nil {
			return err
		}
		ref = digest
	}
	if *trustedKey != "" {
		if _, err := osix.VerifySnapshot(".", ref, osix.VerifyOptions{TrustedKey: *trustedKey}); err != nil {
			return err
		}
	}
	if err := osix.Restore(".", ref, fs.Arg(1), osix.RestoreOptions{Force: *force, Decrypt: *decrypt}); err != nil {
		return err
	}
	fmt.Printf("restored %s to %s\n", fs.Arg(0), fs.Arg(1))
	return nil
}

func runMount(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runMountStatus(args[1:])
		case "recover":
			return runMountRecover(args[1:])
		}
	}
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing non-empty target")
	rw := fs.Bool("rw", true, "create a writable materialized mount")
	branch := fs.String("branch", "", "branch name associated with this mount")
	decrypt := fs.String("decrypt", "", "decrypt identities or KMS recipients, comma-separated")
	mode := fs.String("mode", "auto", "mount mode: auto, overlay, fuse, or materialized")
	cache := fs.String("cache", "", "runtime cache directory")
	lazy := fs.Bool("lazy", false, "defer lowerdir materialization when supported")
	quiet := fs.Bool("quiet", false, "suppress mount details")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"force": true, "rw": true, "lazy": true, "quiet": true}); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: osix mount REF DIR [--mode auto|overlay|fuse|materialized] [--rw] [--branch BRANCH] [--force]")
	}
	ref := fs.Arg(0)
	if osix.IsRegistryReference(ref) {
		digest, err := osix.PullSnapshot(".", ref, "")
		if err != nil {
			return err
		}
		ref = digest
	}
	info, err := osix.Mount(".", ref, fs.Arg(1), osix.MountOptions{
		Force:   *force,
		RW:      *rw,
		Branch:  *branch,
		Decrypt: *decrypt,
		Mode:    osix.MountMode(*mode),
		Cache:   *cache,
		Lazy:    *lazy,
	})
	if err != nil {
		return err
	}
	if !*quiet {
		fmt.Printf("mounted %s at %s\n", info.SourceDigest, fs.Arg(1))
		printMountInfo(info)
	}
	return nil
}

func runMountStatus(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix mount status DIR")
	}
	info, err := osix.NewMountRuntime(".", osix.MountAuto).Status(context.Background(), args[0])
	if err != nil {
		return err
	}
	printMountInfo(info)
	return nil
}

func runMountRecover(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix mount recover DIR")
	}
	info, err := osix.RecoverMount(".", args[0])
	if err != nil {
		return err
	}
	printMountInfo(info)
	return nil
}

func runUnmount(args []string) error {
	fs := flag.NewFlagSet("unmount", flag.ContinueOnError)
	force := fs.Bool("force", false, "force unmount when supported")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"force": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix unmount DIR [--force]")
	}
	if err := osix.NewMountRuntime(".", osix.MountAuto).Unmount(context.Background(), fs.Arg(0), osix.UnmountOptions{Force: *force}); err != nil {
		return err
	}
	fmt.Printf("unmounted %s\n", fs.Arg(0))
	return nil
}

func printMountInfo(info osix.MountInfo) {
	fmt.Printf("target %s\n", info.Target)
	fmt.Printf("source %s\n", info.SourceDigest)
	fmt.Printf("mode %s\n", info.Mode)
	fmt.Printf("state %s\n", info.State)
	if info.UpperDir != "" {
		fmt.Printf("upper %s\n", info.UpperDir)
	}
	if info.LowerDir != "" {
		fmt.Printf("lower %s\n", info.LowerDir)
	}
	if info.WorkDir != "" {
		fmt.Printf("work %s\n", info.WorkDir)
	}
	if info.PID != 0 {
		fmt.Printf("pid %d\n", info.PID)
	}
}

func runDiff(args []string) error {
	var changes []osix.Change
	var err error
	switch len(args) {
	case 1:
		changes, err = osix.DiffMount(".", args[0])
	case 2:
		changes, err = osix.Diff(".", args[0], args[1])
	default:
		return fmt.Errorf("usage: osix diff REF_A REF_B\n   or: osix diff MOUNT_DIR")
	}
	if err != nil {
		return err
	}
	for _, change := range changes {
		fmt.Printf("%s %s\n", change.Kind, change.Path)
	}
	return nil
}

func runFork(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: osix fork SOURCE_REF TARGET_TAG")
	}
	digest, err := osix.Fork(".", args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Printf("tagged %s -> %s\n", args[1], digest)
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	trustedKey := fs.String("trusted-key", "", "trusted ed25519 public key file")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix verify REF [--trusted-key PUBKEY]")
	}
	result, err := osix.VerifySnapshot(".", fs.Arg(0), osix.VerifyOptions{TrustedKey: *trustedKey})
	if err != nil {
		return err
	}
	fmt.Printf("verified %s\n", result.ManifestDigest)
	fmt.Printf("signature %s\n", result.SignatureDigest)
	if result.ProvenanceDigest != "" {
		fmt.Printf("provenance %s\n", result.ProvenanceDigest)
	}
	fmt.Printf("signer %s\n", result.Signer)
	return nil
}

func runValidate(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix validate REF")
	}
	if err := osix.ValidateChain(".", args[0]); err != nil {
		return err
	}
	fmt.Printf("valid chain %s\n", args[0])
	return nil
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	every := fs.Duration("every", 0, "snapshot interval")
	maxDirty := fs.String("max-dirty", "0", "dirty byte threshold, supports KiB/MiB/GiB")
	onTurnBoundary := fs.Bool("on-turn-boundary", false, "wait for turn-boundary hook file")
	push := fs.Bool("push", false, "push snapshots after creation")
	encrypt := fs.String("encrypt", "", "encryption recipients")
	once := fs.Bool("once", false, "run one watch iteration")
	iterations := fs.Int("iterations", 0, "bounded iteration count")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"on-turn-boundary": true, "push": true, "once": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix watch DIR [--once] [--every DURATION] [--max-dirty BYTES] [--on-turn-boundary] [--push]")
	}
	maxDirtyBytes, err := parseBytes(*maxDirty)
	if err != nil {
		return err
	}
	result, err := osix.Watch(".", fs.Arg(0), osix.WatchOptions{
		Every:          *every,
		MaxDirtyBytes:  maxDirtyBytes,
		OnTurnBoundary: *onTurnBoundary,
		Push:           *push,
		Encrypt:        *encrypt,
		Once:           *once,
		Iterations:     *iterations,
	})
	if err != nil {
		return err
	}
	for _, snap := range result.Snapshots {
		fmt.Printf("snapshot %s\n", snap.ManifestDigest)
	}
	fmt.Printf("watch state %s\n", result.StatePath)
	return nil
}

func runCompact(args []string) error {
	fs := flag.NewFlagSet("compact", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "explain compaction without creating checkpoint")
	squashEvery := fs.Int("squash-every", 50, "minimum chain length before checkpointing")
	keepSnapshots := fs.String("keep-snapshots", "", "comma-separated snapshots to preserve")
	preserveSigned := fs.Bool("preserve-signed", false, "preserve signed snapshots")
	checkpointTag := fs.String("tag", "", "checkpoint tag")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"dry-run": true, "preserve-signed": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix compact REF [--dry-run] [--squash-every N] [--keep-snapshots A,B] [--preserve-signed]")
	}
	plan, err := osix.Compact(".", fs.Arg(0), osix.ParseCompactPolicy(*squashEvery, *keepSnapshots, *preserveSigned, *dryRun, *checkpointTag))
	if err != nil {
		return err
	}
	fmt.Printf("source %s\n", plan.SourceDigest)
	fmt.Printf("chain %d\n", plan.ChainLength)
	if plan.CreateCheckpoint {
		fmt.Printf("checkpoint %s", plan.CheckpointTag)
		if plan.CheckpointDigest != "" {
			fmt.Printf(" -> %s", plan.CheckpointDigest)
		}
		fmt.Println()
	}
	for _, reason := range plan.Reasons {
		fmt.Printf("keep %s\n", reason)
	}
	for _, candidate := range plan.DeleteCandidates {
		fmt.Printf("delete-candidate %s\n", candidate)
	}
	return nil
}

func runCommand(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return fmt.Errorf("usage: osix run MOUNT_DIR -- COMMAND [ARG...]")
	}
	cmd := exec.Command(args[2], args[3:]...)
	cmd.Dir = args[0]
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runShow(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix show REF")
	}
	text, err := osix.Show(".", args[0])
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

func parseBytes(input string) (int64, error) {
	input = strings.TrimSpace(input)
	if input == "" || input == "0" {
		return 0, nil
	}
	multiplier := int64(1)
	for suffix, value := range map[string]int64{"KiB": 1024, "MiB": 1024 * 1024, "GiB": 1024 * 1024 * 1024, "KB": 1000, "MB": 1000 * 1000, "GB": 1000 * 1000 * 1000} {
		if strings.HasSuffix(input, suffix) {
			multiplier = value
			input = strings.TrimSuffix(input, suffix)
			break
		}
	}
	var value int64
	if _, err := fmt.Sscanf(input, "%d", &value); err != nil {
		return 0, fmt.Errorf("invalid byte size %q", input)
	}
	return value * multiplier, nil
}

func runRefs(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: osix refs")
	}
	refs, err := osix.Refs(".")
	if err != nil {
		return err
	}
	for _, ref := range refs {
		fmt.Printf("%s %s\n", ref.Name, ref.Digest)
	}
	return nil
}

func parseInterspersed(fs *flag.FlagSet, args []string, boolFlags map[string]bool) error {
	var flags []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			flags = append(flags, arg)
			name := strings.TrimPrefix(arg, "--")
			if before, _, ok := strings.Cut(name, "="); ok {
				name = before
			}
			if !boolFlags[name] && !strings.Contains(arg, "=") {
				if i+1 >= len(args) {
					return fmt.Errorf("flag %s requires a value", arg)
				}
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return fs.Parse(append(flags, positional...))
}
