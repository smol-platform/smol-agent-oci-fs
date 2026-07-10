package k8soperator

//go:generate go run ./cmd/generate-manifests

// RenderInstallManifests returns the install stream generated from the
// canonical deploy/kubernetes Kustomize resources.
func RenderInstallManifests() string {
	return renderedInstallManifests
}
