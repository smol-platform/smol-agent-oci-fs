package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

const usage = `osix is a local prototype for OCI Agent State Images.

Usage:
  osix init BASE --name NAME --state REF --mount DIR [--encrypt RECIPIENTS]
  osix snapshot DIR [--message MSG] [--tag TAG] [--also-tag TAG] [--expected-parent DIGEST] [--encrypt RECIPIENTS] [--sign KEY|keyless|sigstore-keyless] [--attest TYPE]
  osix push REF [REGISTRY/REPO] [--tag TAG] [--expected-parent DIGEST]
  osix registry probe REGISTRY/REPO [--tag TAG] [--json]
  osix pull REGISTRY/REPO:TAG [--tag LOCAL_TAG] [--lazy]
  osix read REF PATH [--decrypt IDENTITIES] [--offset N --length N]
  osix restore REF DIR [--force] [--decrypt IDENTITIES] [--trusted-key PUBKEY] [--certificate-identity ID --certificate-oidc-issuer ISSUER]
  osix mount REF DIR [--mode auto|overlay|fuse|materialized] [--rw] [--branch BRANCH] [--force] [--decrypt IDENTITIES] [--cache DIR] [--lazy]
  osix mount status DIR
  osix mount recover DIR
  osix unmount DIR [--force]
  osix diff REF_A REF_B
  osix diff MOUNT_DIR
  osix fork SOURCE_REF TARGET_TAG
  osix verify REF [--trusted-key PUBKEY] [--certificate-identity ID --certificate-oidc-issuer ISSUER]
  osix validate REF
  osix watch DIR [--once] [--every DURATION] [--max-dirty BYTES] [--on-turn-boundary] [--push] [--compact-every N] [--squash-every N] [--prune-local] [--prune-remote]
  osix watch start/status/stop/list/restart
  osix compact REF [--dry-run] [--squash-every N] [--keep-snapshots A,B] [--preserve-signed] [--prune-local]
  osix side-effect check DIR --tool TOOL --resource RESOURCE [--operation read|write] [--idempotency-key KEY]
  osix run MOUNT_DIR -- COMMAND [ARG...]
  osix show REF
  osix refs

Refs are local tags, immutable sha256:... manifest digests, or remote OCI refs
such as localhost:5000/acme/agent-state:snap-000001.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "osix: %v\n", err)
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	if osix.IsRemoteBranchConflict(err) {
		return 3
	}
	return 1
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "__fuse-lazy-server":
		return runFuseLazyServer(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "push":
		return runPush(args[1:])
	case "registry":
		return runRegistry(args[1:])
	case "pull":
		return runPull(args[1:])
	case "read":
		return runRead(args[1:])
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
	case "side-effect":
		return runSideEffect(args[1:])
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

func runRegistry(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: osix registry probe REGISTRY/REPO [--tag TAG] [--json]")
	}
	switch args[0] {
	case "probe":
		return runRegistryProbe(args[1:])
	default:
		return fmt.Errorf("unknown registry command %q", args[0])
	}
}

func runRegistryProbe(args []string) error {
	fs := flag.NewFlagSet("registry probe", flag.ContinueOnError)
	tag := fs.String("tag", "", "probe tag to publish")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"json": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix registry probe REGISTRY/REPO [--tag TAG] [--json]")
	}
	result, err := osix.ProbeRegistryAccess(fs.Arg(0), osix.RegistryProbeOptions{Tag: *tag})
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	fmt.Printf("registry probe passed for %s:%s\n", result.Repository, result.Tag)
	fmt.Printf("manifest %s\n", result.ManifestDigest)
	fmt.Printf("layer %s\n", result.LayerDigest)
	return nil
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
	expectedParent := fs.String("expected-parent", "", "expected current remote digest before mutable tag updates")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("usage: osix push REF [REGISTRY/REPO] [--tag TAG] [--expected-parent DIGEST]")
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
	if err := osix.PushSnapshotWithOptions(".", remoteRepo, ref, tags, osix.PushOptions{ExpectedParent: *expectedParent}); err != nil {
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
	lazy := fs.Bool("lazy", false, "fetch manifests/configs now and layers on demand")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"lazy": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix pull REGISTRY/REPO:TAG [--tag LOCAL_TAG] [--lazy]")
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
	digest, err := osix.PullSnapshotWithOptions(".", remoteRef, *localTag, osix.PullOptions{Lazy: *lazy})
	if err != nil {
		return err
	}
	fmt.Printf("pulled %s\n", digest)
	if *localTag != "" {
		fmt.Printf("tagged %s -> %s\n", *localTag, digest)
	}
	return nil
}

func runRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	decrypt := fs.String("decrypt", "", "decrypt identities or KMS recipients, comma-separated")
	offset := fs.Int64("offset", 0, "start byte offset for a range read")
	length := fs.Int64("length", -1, "number of bytes to read; enables range reads when set")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: osix read REF PATH [--decrypt IDENTITIES] [--offset N --length N]")
	}
	if *offset < 0 {
		return fmt.Errorf("--offset must be non-negative")
	}
	if *length < 0 && *offset != 0 {
		return fmt.Errorf("--length is required when --offset is set")
	}
	var data []byte
	var err error
	if *length >= 0 {
		data, err = osix.ReadSnapshotFileRange(".", fs.Arg(0), fs.Arg(1), *offset, *length, osix.ReadFileOptions{Decrypt: *decrypt})
	} else {
		data, err = osix.ReadSnapshotFile(".", fs.Arg(0), fs.Arg(1), osix.ReadFileOptions{Decrypt: *decrypt})
	}
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	msg := fs.String("message", "", "snapshot message")
	tag := fs.String("tag", "", "tag to write")
	alsoTag := fs.String("also-tag", "", "additional tag to write")
	encrypt := fs.String("encrypt", "", "encryption recipients, comma-separated")
	sign := fs.String("sign", "", "sign manifest digest with key path, keyless, or sigstore-keyless")
	attest := fs.String("attest", "", "provenance attestation label")
	sigstoreSign := addSigstoreSigningFlags(fs)
	expectedParent := fs.String("expected-parent", "", "expected current digest for mutable tag updates")
	secretScan := fs.String("secret-scan", "warn", "secret scan mode: block, warn, or off")
	push := fs.Bool("push", false, "push snapshot to workspace state registry repo")
	fs.SetOutput(os.Stderr)
	boolFlags := sigstoreSigningBoolFlags()
	boolFlags["push"] = true
	if err := parseInterspersed(fs, args, boolFlags); err != nil {
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
		Sigstore:       sigstoreSign,
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
		if err := osix.PushSnapshotWithOptions(".", cfg.StateRef, result.ManifestDigest, result.Tags, osix.PushOptions{ExpectedParent: *expectedParent}); err != nil {
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

func addSigstoreSigningFlags(fs *flag.FlagSet) osix.SigstoreSignOptions {
	var opts osix.SigstoreSignOptions
	fs.StringVar(&opts.IdentityToken, "sigstore-identity-token", "", "OIDC identity token for sigstore-keyless signing")
	fs.StringVar(&opts.IdentityTokenFile, "sigstore-identity-token-file", "", "file containing OIDC identity token for sigstore-keyless signing")
	fs.StringVar(&opts.TrustedRoot, "sigstore-trusted-root", "", "Sigstore trusted_root.json path")
	fs.StringVar(&opts.SigningConfig, "sigstore-signing-config", "", "Sigstore signing_config.json path")
	fs.StringVar(&opts.TUFCache, "sigstore-tuf-cache", "", "Sigstore TUF cache directory")
	fs.StringVar(&opts.TUFURL, "sigstore-tuf-url", "", "Sigstore TUF repository URL")
	fs.BoolVar(&opts.TUFStaging, "sigstore-tuf-staging", false, "use Sigstore staging TUF root and repository")
	fs.StringVar(&opts.FulcioURL, "sigstore-fulcio-url", "", "Fulcio base URL for sigstore-keyless signing")
	fs.StringVar(&opts.RekorURL, "sigstore-rekor-url", "", "Rekor base URL for sigstore-keyless signing")
	fs.StringVar(&opts.TimestampURL, "sigstore-timestamp-url", "", "timestamp authority URL for sigstore-keyless signing")
	fs.BoolVar(&opts.NoRekor, "sigstore-no-rekor", false, "do not request a Rekor transparency-log entry during sigstore-keyless signing")
	fs.BoolVar(&opts.NoTimestamp, "sigstore-no-timestamp", false, "do not request an RFC3161 timestamp during sigstore-keyless signing")
	return opts
}

func sigstoreSigningBoolFlags() map[string]bool {
	return map[string]bool{
		"sigstore-tuf-staging":  true,
		"sigstore-no-rekor":     true,
		"sigstore-no-timestamp": true,
	}
}

func runRestore(args []string, mountAlias bool) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing non-empty target")
	decrypt := fs.String("decrypt", "", "decrypt identities or KMS recipients, comma-separated")
	verifyOpts := addVerifyFlags(fs)
	fs.SetOutput(os.Stderr)
	boolFlags := verifyBoolFlags()
	boolFlags["force"] = true
	if err := parseInterspersed(fs, args, boolFlags); err != nil {
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
	if verifyRequested(verifyOpts) {
		if _, err := osix.VerifySnapshot(".", ref, verifyOpts); err != nil {
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
		digest, err := osix.PullSnapshotWithOptions(".", ref, "", osix.PullOptions{Lazy: *lazy})
		if err != nil {
			return err
		}
		ref = digest
	}
	info, err := osix.Mount(".", ref, fs.Arg(1), osix.MountOptions{
		Force:    *force,
		RW:       *rw,
		ReadOnly: !*rw,
		Branch:   *branch,
		Decrypt:  *decrypt,
		Mode:     osix.MountMode(*mode),
		Cache:    *cache,
		Lazy:     *lazy,
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

func runFuseLazyServer(args []string) error {
	fs := flag.NewFlagSet("__fuse-lazy-server", flag.ContinueOnError)
	workspaceRoot := fs.String("workspace-root", ".", "workspace root")
	sourceRef := fs.String("source-ref", "", "snapshot reference")
	target := fs.String("target", "", "mount target")
	upper := fs.String("upper", "", "upperdir for writable lazy FUSE state")
	readOnly := fs.Bool("read-only", false, "mount the lazy FUSE server read-only")
	decrypt := fs.String("decrypt", "", "decrypt identities or KMS recipients, comma-separated")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"read-only": true}); err != nil {
		return err
	}
	if *sourceRef == "" || *target == "" || *upper == "" {
		return fmt.Errorf("usage: osix __fuse-lazy-server --workspace-root DIR --source-ref REF --target DIR --upper DIR [--read-only] [--decrypt IDENTITIES]")
	}
	return osix.RunLazyFUSEServer(context.Background(), *workspaceRoot, *sourceRef, *target, *upper, *readOnly, osix.ReadFileOptions{Decrypt: *decrypt})
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
	verifyOpts := addVerifyFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, verifyBoolFlags()); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix verify REF [--trusted-key PUBKEY]")
	}
	result, err := osix.VerifySnapshot(".", fs.Arg(0), verifyOpts)
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

func addVerifyFlags(fs *flag.FlagSet) osix.VerifyOptions {
	var opts osix.VerifyOptions
	fs.StringVar(&opts.TrustedKey, "trusted-key", "", "trusted ed25519 base64 or ECDSA P-256 PEM public key file")
	fs.StringVar(&opts.CertificateIdentity, "certificate-identity", "", "expected Sigstore certificate subject alternative name")
	fs.StringVar(&opts.CertificateIdentityRegexp, "certificate-identity-regexp", "", "expected Sigstore certificate subject alternative name regexp")
	fs.StringVar(&opts.CertificateOIDCIssuer, "certificate-oidc-issuer", "", "expected Sigstore OIDC issuer")
	fs.StringVar(&opts.CertificateOIDCIssuerRegexp, "certificate-oidc-issuer-regexp", "", "expected Sigstore OIDC issuer regexp")
	fs.StringVar(&opts.SigstoreTrustedRoot, "sigstore-trusted-root", "", "Sigstore trusted_root.json path")
	fs.StringVar(&opts.SigstoreTUFCache, "sigstore-tuf-cache", "", "Sigstore TUF cache directory")
	fs.StringVar(&opts.SigstoreTUFURL, "sigstore-tuf-url", "", "Sigstore TUF repository URL")
	fs.BoolVar(&opts.SigstoreTUFStaging, "sigstore-tuf-staging", false, "use Sigstore staging TUF root and repository")
	fs.BoolVar(&opts.SigstoreIgnoreTlog, "sigstore-ignore-tlog", false, "do not require Rekor transparency log inclusion")
	fs.BoolVar(&opts.SigstoreIgnoreTimestamp, "sigstore-ignore-timestamp", false, "use current time instead of requiring observer timestamp material")
	fs.BoolVar(&opts.SigstoreIgnoreCertificateSCT, "sigstore-ignore-certificate-sct", false, "do not require Fulcio certificate SCT verification")
	return opts
}

func verifyRequested(opts osix.VerifyOptions) bool {
	return opts.TrustedKey != "" ||
		opts.CertificateIdentity != "" ||
		opts.CertificateIdentityRegexp != "" ||
		opts.CertificateOIDCIssuer != "" ||
		opts.CertificateOIDCIssuerRegexp != "" ||
		opts.SigstoreTrustedRoot != "" ||
		opts.SigstoreTUFCache != "" ||
		opts.SigstoreTUFURL != "" ||
		opts.SigstoreTUFStaging ||
		opts.SigstoreIgnoreTlog ||
		opts.SigstoreIgnoreTimestamp ||
		opts.SigstoreIgnoreCertificateSCT
}

func verifyBoolFlags() map[string]bool {
	return map[string]bool{
		"sigstore-tuf-staging":            true,
		"sigstore-ignore-tlog":            true,
		"sigstore-ignore-timestamp":       true,
		"sigstore-ignore-certificate-sct": true,
	}
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
	if len(args) > 0 {
		switch args[0] {
		case "start":
			return runWatchStart(args[1:])
		case "status":
			return runWatchStatus(args[1:])
		case "stop":
			return runWatchStop(args[1:])
		case "list":
			return runWatchList(args[1:])
		case "restart":
			return runWatchRestart(args[1:])
		case "run-daemon":
			return runWatchDaemon(args[1:])
		}
	}
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	every := fs.Duration("every", 0, "snapshot interval")
	maxDirty := fs.String("max-dirty", "0", "dirty byte threshold, supports KiB/MiB/GiB")
	onTurnBoundary := fs.Bool("on-turn-boundary", false, "wait for turn-boundary hook file")
	push := fs.Bool("push", false, "push snapshots after creation")
	encrypt := fs.String("encrypt", "", "encryption recipients")
	once := fs.Bool("once", false, "run one watch iteration")
	iterations := fs.Int("iterations", 0, "bounded iteration count")
	retentionFlags := addWatchRetentionFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, watchBoolFlags("once")); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix watch DIR [--once] [--every DURATION] [--max-dirty BYTES] [--on-turn-boundary] [--push] [--compact-every N] [--squash-every N] [--prune-local] [--prune-remote]")
	}
	maxDirtyBytes, err := parseBytes(*maxDirty)
	if err != nil {
		return err
	}
	retention := retentionPolicyFromFlags(retentionFlags)
	result, err := osix.Watch(".", fs.Arg(0), osix.WatchOptions{
		Every:          *every,
		MaxDirtyBytes:  maxDirtyBytes,
		OnTurnBoundary: *onTurnBoundary,
		Push:           *push,
		Encrypt:        *encrypt,
		Retention:      retention,
		Once:           *once,
		Iterations:     *iterations,
	})
	if err != nil {
		return err
	}
	for _, snap := range result.Snapshots {
		fmt.Printf("snapshot %s\n", snap.ManifestDigest)
	}
	for _, plan := range result.Compactions {
		printCompactPlan(plan)
	}
	fmt.Printf("watch state %s\n", result.StatePath)
	return nil
}

func runWatchStart(args []string) error {
	fs := flag.NewFlagSet("watch start", flag.ContinueOnError)
	every := fs.Duration("every", time.Minute, "snapshot interval")
	maxDirty := fs.String("max-dirty", "0", "dirty byte threshold, supports KiB/MiB/GiB")
	onTurnBoundary := fs.Bool("on-turn-boundary", false, "wait for turn-boundary hook file")
	push := fs.Bool("push", false, "push snapshots after creation")
	encrypt := fs.String("encrypt", "", "encryption recipients")
	retentionFlags := addWatchRetentionFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, watchBoolFlags()); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix watch start DIR [--every DURATION] [--max-dirty BYTES] [--on-turn-boundary] [--push] [--compact-every N] [--squash-every N] [--prune-local] [--prune-remote]")
	}
	maxDirtyBytes, err := parseBytes(*maxDirty)
	if err != nil {
		return err
	}
	target := fs.Arg(0)
	opts := osix.WatchOptions{Every: *every, MaxDirtyBytes: maxDirtyBytes, OnTurnBoundary: *onTurnBoundary, Push: *push, Encrypt: *encrypt, Retention: retentionPolicyFromFlags(retentionFlags)}
	return startWatchDaemon(target, opts, every.String(), *maxDirty)
}

func startWatchDaemon(target string, opts osix.WatchOptions, everyArg, maxDirtyArg string) error {
	record, err := osix.PrepareWatchDaemon(".", target, opts)
	if err != nil {
		return err
	}
	return startPreparedWatchDaemon(record, target, everyArg, maxDirtyArg)
}

func startPreparedWatchDaemon(record osix.WatchDaemonRecord, target, everyArg, maxDirtyArg string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	childArgs := []string{"watch", "run-daemon", target, "--every", everyArg, "--max-dirty", maxDirtyArg}
	if record.OnTurnBoundary {
		childArgs = append(childArgs, "--on-turn-boundary")
	}
	if record.Push {
		childArgs = append(childArgs, "--push")
	}
	if record.Encrypt != "" {
		childArgs = append(childArgs, "--encrypt", record.Encrypt)
	}
	childArgs = appendRetentionArgs(childArgs, record.Retention)
	cmd := exec.Command(exe, childArgs...)
	if cwd, err := os.Getwd(); err == nil {
		cmd.Dir = cwd
	}
	logFile, err := os.OpenFile(record.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := osix.MarkWatchDaemonRunning(record, cmd.Process.Pid); err != nil {
		return err
	}
	fmt.Printf("watch daemon %s pid %d\n", record.ID, cmd.Process.Pid)
	fmt.Printf("watch state %s\n", record.StatePath)
	fmt.Printf("watch log %s\n", record.LogPath)
	return nil
}

func runWatchRestart(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix watch restart DIR")
	}
	record, err := osix.PrepareWatchDaemonRestart(".", args[0])
	if err != nil {
		return err
	}
	return startPreparedWatchDaemon(record, args[0], record.Every.String(), fmt.Sprintf("%d", record.MaxDirtyBytes))
}

func runWatchDaemon(args []string) error {
	fs := flag.NewFlagSet("watch run-daemon", flag.ContinueOnError)
	every := fs.Duration("every", time.Minute, "snapshot interval")
	maxDirty := fs.String("max-dirty", "0", "dirty byte threshold, supports KiB/MiB/GiB")
	onTurnBoundary := fs.Bool("on-turn-boundary", false, "wait for turn-boundary hook file")
	push := fs.Bool("push", false, "push snapshots after creation")
	encrypt := fs.String("encrypt", "", "encryption recipients")
	retentionFlags := addWatchRetentionFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, watchBoolFlags()); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix watch run-daemon DIR [--every DURATION] [--max-dirty BYTES] [--on-turn-boundary] [--push] [--compact-every N] [--squash-every N] [--prune-local] [--prune-remote]")
	}
	maxDirtyBytes, err := parseBytes(*maxDirty)
	if err != nil {
		return err
	}
	record, err := osix.WatchDaemonStatus(".", fs.Arg(0))
	if err != nil {
		return err
	}
	if err := osix.MarkWatchDaemonRunning(record, os.Getpid()); err != nil {
		return err
	}
	opts := osix.WatchOptions{
		Every:          *every,
		MaxDirtyBytes:  maxDirtyBytes,
		OnTurnBoundary: *onTurnBoundary,
		Push:           *push,
		Encrypt:        *encrypt,
		Retention:      retentionPolicyFromFlags(retentionFlags),
		UntilStopped:   true,
		StopPath:       record.StopPath,
	}
	result, runErr := osix.Watch(".", fs.Arg(0), opts)
	if err := osix.CompleteWatchDaemon(record, result, runErr); err != nil {
		return err
	}
	return runErr
}

func runWatchStatus(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix watch status DIR")
	}
	record, err := osix.WatchDaemonStatus(".", args[0])
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func runWatchList(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: osix watch list")
	}
	records, err := osix.WatchDaemonList(".")
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func runWatchStop(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix watch stop DIR")
	}
	record, err := osix.StopWatchDaemon(".", args[0])
	if err != nil {
		return err
	}
	if record.PID > 0 {
		if process, err := os.FindProcess(record.PID); err == nil {
			_ = process.Signal(os.Interrupt)
		}
	}
	fmt.Printf("watch daemon %s stopping\n", record.ID)
	fmt.Printf("stop file %s\n", record.StopPath)
	return nil
}

func runCompact(args []string) error {
	fs := flag.NewFlagSet("compact", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "explain compaction without creating checkpoint")
	squashEvery := fs.Int("squash-every", 50, "minimum chain length before checkpointing")
	keepSnapshots := fs.String("keep-snapshots", "", "comma-separated snapshots to preserve")
	preserveSigned := fs.Bool("preserve-signed", false, "preserve signed snapshots")
	checkpointTag := fs.String("tag", "", "checkpoint tag")
	pruneLocal := fs.Bool("prune-local", false, "remove unretained local refs and blobs after checkpointing")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"dry-run": true, "preserve-signed": true, "prune-local": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix compact REF [--dry-run] [--squash-every N] [--keep-snapshots A,B] [--preserve-signed]")
	}
	plan, err := osix.Compact(".", fs.Arg(0), osix.CompactPolicy{
		SquashEvery:    *squashEvery,
		KeepSnapshots:  splitCSV(*keepSnapshots),
		PreserveSigned: *preserveSigned,
		DryRun:         *dryRun,
		CheckpointTag:  *checkpointTag,
		PruneLocal:     *pruneLocal,
	})
	if err != nil {
		return err
	}
	printCompactPlan(plan)
	return nil
}

func printCompactPlan(plan osix.CompactPlan) {
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
	for _, ref := range plan.PrunedRefs {
		fmt.Printf("pruned-ref %s\n", ref)
	}
	for _, blob := range plan.PrunedBlobs {
		fmt.Printf("pruned-blob %s\n", blob)
	}
	for _, digest := range plan.RemoteDeleteCandidates {
		fmt.Printf("remote-delete-candidate %s\n", digest)
	}
	for _, digest := range plan.RemoteDeleted {
		fmt.Printf("remote-deleted %s\n", digest)
	}
}

type watchRetentionFlags struct {
	compactEvery        *int
	squashEvery         *int
	checkpointTagPrefix *string
	keepSnapshots       *string
	preserveSigned      *bool
	pruneLocal          *bool
	pruneRemote         *bool
	dryRun              *bool
}

func addWatchRetentionFlags(fs *flag.FlagSet) watchRetentionFlags {
	return watchRetentionFlags{
		compactEvery:        fs.Int("compact-every", 0, "run compaction every N watch snapshots; 0 disables retention compaction"),
		squashEvery:         fs.Int("squash-every", 50, "minimum chain length before creating a checkpoint"),
		checkpointTagPrefix: fs.String("checkpoint-tag-prefix", "checkpoint", "prefix for watch-created checkpoint tags"),
		keepSnapshots:       fs.String("keep-snapshots", "", "comma-separated snapshots to preserve during retention pruning"),
		preserveSigned:      fs.Bool("preserve-signed", false, "preserve signed snapshots during retention pruning"),
		pruneLocal:          fs.Bool("prune-local", false, "remove unretained local refs and blobs after checkpointing"),
		pruneRemote:         fs.Bool("prune-remote", false, "delete unretained remote manifests after pushing a checkpoint"),
		dryRun:              fs.Bool("retention-dry-run", false, "plan retention compaction without creating checkpoints or pruning"),
	}
}

func retentionPolicyFromFlags(flags watchRetentionFlags) osix.WatchRetentionPolicy {
	return osix.WatchRetentionPolicy{
		CompactEvery:        *flags.compactEvery,
		SquashEvery:         *flags.squashEvery,
		CheckpointTagPrefix: *flags.checkpointTagPrefix,
		KeepSnapshots:       splitCSV(*flags.keepSnapshots),
		PreserveSigned:      *flags.preserveSigned,
		PruneLocal:          *flags.pruneLocal,
		PruneRemote:         *flags.pruneRemote,
		DryRun:              *flags.dryRun,
	}
}

func watchBoolFlags(extra ...string) map[string]bool {
	flags := map[string]bool{
		"on-turn-boundary":  true,
		"push":              true,
		"preserve-signed":   true,
		"prune-local":       true,
		"prune-remote":      true,
		"retention-dry-run": true,
	}
	for _, name := range extra {
		flags[name] = true
	}
	return flags
}

func appendRetentionArgs(args []string, retention osix.WatchRetentionPolicy) []string {
	if retention.CompactEvery > 0 {
		args = append(args, "--compact-every", fmt.Sprintf("%d", retention.CompactEvery))
	}
	if retention.SquashEvery > 0 {
		args = append(args, "--squash-every", fmt.Sprintf("%d", retention.SquashEvery))
	}
	if retention.CheckpointTagPrefix != "" {
		args = append(args, "--checkpoint-tag-prefix", retention.CheckpointTagPrefix)
	}
	if len(retention.KeepSnapshots) > 0 {
		args = append(args, "--keep-snapshots", strings.Join(retention.KeepSnapshots, ","))
	}
	if retention.PreserveSigned {
		args = append(args, "--preserve-signed")
	}
	if retention.PruneLocal {
		args = append(args, "--prune-local")
	}
	if retention.PruneRemote {
		args = append(args, "--prune-remote")
	}
	if retention.DryRun {
		args = append(args, "--retention-dry-run")
	}
	return args
}

func splitCSV(input string) []string {
	var out []string
	for _, item := range strings.Split(input, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
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

func runSideEffect(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: osix side-effect check DIR --tool TOOL --resource RESOURCE [--operation read|write] [--idempotency-key KEY]")
	}
	switch args[0] {
	case "check":
		return runSideEffectCheck(args[1:])
	default:
		return fmt.Errorf("unknown side-effect command %q", args[0])
	}
}

func runSideEffectCheck(args []string) error {
	fs := flag.NewFlagSet("side-effect check", flag.ContinueOnError)
	tool := fs.String("tool", "", "external tool name")
	resource := fs.String("resource", "", "external resource identifier")
	operation := fs.String("operation", "write", "operation: read or write")
	idempotencyKey := fs.String("idempotency-key", "", "idempotency key for idempotent retry checks")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix side-effect check DIR --tool TOOL --resource RESOURCE [--operation read|write] [--idempotency-key KEY]")
	}
	decision, err := osix.CheckSideEffect(fs.Arg(0), osix.SideEffectCheck{
		Tool:             *tool,
		ExternalResource: *resource,
		Operation:        *operation,
		IdempotencyKey:   *idempotencyKey,
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(decision, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	switch decision.Action {
	case osix.SideEffectActionAllow, osix.SideEffectActionMock:
		return nil
	default:
		return fmt.Errorf("side-effect action %s: %s", decision.Action, decision.Reason)
	}
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
