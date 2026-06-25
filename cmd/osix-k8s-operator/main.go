package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
)

const usage = `osix-k8s-operator manages OSIx Kubernetes resources.

Usage:
  osix-k8s-operator render-install
  osix-k8s-operator plan --name NAME --state REF --base IMAGE --target DIR --workspace-root DIR [--source REF] [--mode MODE]
  osix-k8s-operator serve [--addr ADDR]
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "osix-k8s-operator: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "render-install":
		fmt.Print(k8soperator.RenderInstallManifests())
		return nil
	case "plan":
		return runPlan(args[1:])
	case "serve":
		return runServe(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	name := fs.String("name", "", "filesystem name")
	namespace := fs.String("namespace", "default", "filesystem namespace")
	stateRef := fs.String("state", "", "OCI state repository")
	baseImage := fs.String("base", "", "base image")
	sourceRef := fs.String("source", "", "source branch, digest, or remote ref")
	mode := fs.String("mode", "auto", "mount mode")
	target := fs.String("target", "", "target path")
	workspaceRoot := fs.String("workspace-root", "", "workspace root")
	volumeID := fs.String("volume-id", "", "volume id")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	filesystem := k8soperator.NormalizeFileSystem(k8soperator.AgentOCIFileSystem{
		TypeMeta:   k8soperator.TypeMeta{APIVersion: k8soperator.APIVersion, Kind: k8soperator.KindAgentOCIFileSystem},
		ObjectMeta: k8soperator.ObjectMeta{Name: *name, Namespace: *namespace},
		Spec: k8soperator.AgentOCIFileSystemSpec{
			BaseImage: *baseImage,
			StateRef:  *stateRef,
			SourceRef: *sourceRef,
			MountMode: *mode,
		},
	})
	plan, err := k8soperator.PublishPlan(filesystem, k8soperator.VolumePlanOptions{
		WorkspaceRoot: *workspaceRoot,
		TargetPath:    *target,
		VolumeID:      *volumeID,
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "health listen address")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","component":"osix-k8s-operator"}` + "\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready","component":"osix-k8s-operator"}` + "\n"))
	})
	return http.ListenAndServe(*addr, mux)
}
