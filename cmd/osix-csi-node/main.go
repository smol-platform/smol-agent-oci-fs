package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/smol-platform/smol-agent-oci-fs/internal/csinode"
	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
)

const usage = `osix-csi-node is the OSIx Kubernetes CSI node runtime.

Usage:
  osix-csi-node publish --workspace-root DIR --target DIR --volume-id ID --name NAME --state REF --base IMAGE [--source REF] [--mode auto|overlay|fuse|materialized]
  osix-csi-node snapshot --workspace-root DIR --target DIR --volume-id ID --name NAME --state REF --base IMAGE [--push] [--compact-every N] [--squash-every N] [--prune-local] [--prune-remote]
  osix-csi-node unpublish --workspace-root DIR --target DIR --volume-id ID --name NAME --state REF --base IMAGE [--final-snapshot]
  osix-csi-node serve [--addr ADDR]
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "osix-csi-node: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "publish":
		return runPublish(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "unpublish":
		return runUnpublish(args[1:])
	case "serve":
		return runServe(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type nodeFlags struct {
	workspaceRoot string
	target        string
	volumeID      string
	name          string
	namespace     string
	stateRef      string
	baseImage     string
	sourceRef     string
	branch        string
	mode          string
}

func addNodeFlags(fs *flag.FlagSet) *nodeFlags {
	flags := &nodeFlags{}
	fs.StringVar(&flags.workspaceRoot, "workspace-root", "", "node-local workspace root")
	fs.StringVar(&flags.target, "target", "", "pod target path")
	fs.StringVar(&flags.volumeID, "volume-id", "", "CSI volume id")
	fs.StringVar(&flags.name, "name", "", "AgentOCIFileSystem name")
	fs.StringVar(&flags.namespace, "namespace", "default", "AgentOCIFileSystem namespace")
	fs.StringVar(&flags.stateRef, "state", "", "OCI state repository")
	fs.StringVar(&flags.baseImage, "base", "", "base image")
	fs.StringVar(&flags.sourceRef, "source", "", "source branch, digest, or remote ref")
	fs.StringVar(&flags.branch, "branch", "main", "branch tag")
	fs.StringVar(&flags.mode, "mode", "materialized", "mount mode")
	return flags
}

func (f nodeFlags) fileSystem() k8soperator.AgentOCIFileSystem {
	return k8soperator.NormalizeFileSystem(k8soperator.AgentOCIFileSystem{
		TypeMeta:   k8soperator.TypeMeta{APIVersion: k8soperator.APIVersion, Kind: k8soperator.KindAgentOCIFileSystem},
		ObjectMeta: k8soperator.ObjectMeta{Name: f.name, Namespace: f.namespace},
		Spec: k8soperator.AgentOCIFileSystemSpec{
			BaseImage: f.baseImage,
			StateRef:  f.stateRef,
			Branch:    f.branch,
			SourceRef: f.sourceRef,
			MountMode: f.mode,
		},
	})
}

func runPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	flags := addNodeFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	node := csinode.Node{WorkspaceRoot: flags.workspaceRoot}
	result, err := node.Publish(context.Background(), csinode.PublishRequest{
		FileSystem: flags.fileSystem(),
		VolumeID:   flags.volumeID,
		TargetPath: flags.target,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags := addNodeFlags(fs)
	policy := addSnapshotPolicyFlags(fs)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	node := csinode.Node{WorkspaceRoot: flags.workspaceRoot}
	result, err := node.Snapshot(context.Background(), csinode.SnapshotRequest{
		FileSystem: flags.fileSystem(),
		Policy:     policy,
		VolumeID:   flags.volumeID,
		TargetPath: flags.target,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func runUnpublish(args []string) error {
	fs := flag.NewFlagSet("unpublish", flag.ContinueOnError)
	flags := addNodeFlags(fs)
	finalSnapshot := fs.Bool("final-snapshot", false, "take a final snapshot before unpublish")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	node := csinode.Node{WorkspaceRoot: flags.workspaceRoot}
	return node.Unpublish(context.Background(), csinode.PublishRequest{
		FileSystem: flags.fileSystem(),
		VolumeID:   flags.volumeID,
		TargetPath: flags.target,
	}, *finalSnapshot)
}

func addSnapshotPolicyFlags(fs *flag.FlagSet) *k8soperator.AgentOCISnapshotPolicySpec {
	policy := &k8soperator.AgentOCISnapshotPolicySpec{}
	fs.StringVar(&policy.Every, "every", "", "snapshot interval")
	fs.StringVar(&policy.MaxDirtyBytes, "max-dirty", "0", "dirty byte threshold")
	fs.BoolVar(&policy.OnTurnBoundary, "on-turn-boundary", false, "wait for turn-boundary marker")
	fs.BoolVar(&policy.Push, "push", true, "push snapshots after creation")
	fs.IntVar(&policy.CompactEvery, "compact-every", 0, "run compaction every N snapshots")
	fs.IntVar(&policy.SquashEvery, "squash-every", 50, "chain length threshold for checkpoint")
	fs.StringVar(&policy.CheckpointTagPrefix, "checkpoint-tag-prefix", "checkpoint", "checkpoint tag prefix")
	fs.BoolVar(&policy.PreserveSigned, "preserve-signed", false, "preserve signed snapshots")
	fs.BoolVar(&policy.PruneLocal, "prune-local", false, "prune local refs/blobs")
	fs.BoolVar(&policy.PruneRemote, "prune-remote", false, "prune remote manifests")
	return policy
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8081", "health listen address")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","component":"osix-csi-node"}` + "\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready","component":"osix-csi-node"}` + "\n"))
	})
	return http.ListenAndServe(*addr, mux)
}
