package csinode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

func TestRegistryCredentialsProjectedTemporarily(t *testing.T) {
	t.Setenv("OSIX_REGISTRY_USERNAME", "existing")
	t.Setenv("OSIX_REGISTRY_PASSWORD", "")
	t.Setenv("OSIX_REGISTRY_TOKEN", "")
	t.Setenv("DOCKER_CONFIG", "")
	fs := testFileSystem("agent-creds")
	fs.Spec.RegistrySecretRef = &k8soperator.LocalObjectReference{Name: "registry-auth"}
	node := Node{
		WorkspaceRoot:  t.TempDir(),
		SecretProvider: fakeSecretProvider{data: map[string][]byte{"username": []byte("robot"), "password": []byte("secret"), ".dockerconfigjson": []byte(`{"auths":{"registry.example":{"auth":"abc"}}}`)}},
	}
	err := node.withRegistryCredentials(context.Background(), fs, func() error {
		if got := os.Getenv("OSIX_REGISTRY_USERNAME"); got != "robot" {
			t.Fatalf("username env = %q", got)
		}
		if got := os.Getenv("OSIX_REGISTRY_PASSWORD"); got != "secret" {
			t.Fatalf("password env = %q", got)
		}
		configPath := filepath.Join(os.Getenv("DOCKER_CONFIG"), "config.json")
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "registry.example") {
			t.Fatalf("docker config not projected: %s", data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("OSIX_REGISTRY_USERNAME"); got != "existing" {
		t.Fatalf("username env was not restored: %q", got)
	}
	if got := os.Getenv("OSIX_REGISTRY_PASSWORD"); got != "" {
		t.Fatalf("password env leaked after projection: %q", got)
	}
	record := MountRecord{FileSystem: fs}
	recordData, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"robot", "secret", "registry.example"} {
		if strings.Contains(string(recordData), leaked) {
			t.Fatalf("mount record leaked credential value %q: %s", leaked, recordData)
		}
	}
}

func TestKubernetesSecretProviderDecodesSecretData(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("service-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/agents/secrets/registry-auth" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer service-token" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{
				"username": base64.StdEncoding.EncodeToString([]byte("robot")),
				"password": base64.StdEncoding.EncodeToString([]byte("secret")),
			},
		})
	}))
	defer server.Close()
	provider := &KubernetesSecretProvider{Host: server.URL, TokenPath: tokenPath, Client: http.DefaultClient}
	data, err := provider.SecretData(context.Background(), "agents", "registry-auth")
	if err != nil {
		t.Fatal(err)
	}
	if string(data["username"]) != "robot" || string(data["password"]) != "secret" {
		t.Fatalf("decoded secret data = %#v", data)
	}
}

func TestVerifySourceRequiresSignatureWhenSigningPolicySet(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "fs")
	if _, err := osix.Init(root, osix.InitOptions{Base: "base", Name: "agent-verify", StateRef: "local/agent-verify", Mount: target, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "unsigned\n")
	unsigned, err := osix.Snapshot(root, target, osix.SnapshotOptions{Tag: "unsigned"})
	if err != nil {
		t.Fatal(err)
	}
	node := Node{WorkspaceRoot: filepath.Join(root, "nodes")}
	fs := testFileSystem("agent-verify")
	fs.Spec.Signing = &k8soperator.SigningSpec{Signer: "keyless"}
	if err := node.verifySource(context.Background(), fs, root, unsigned.ManifestDigest); err == nil {
		t.Fatal("expected unsigned source verification to fail")
	}
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "signed\n")
	signed, err := osix.Snapshot(root, target, osix.SnapshotOptions{Tag: "signed", Sign: "keyless", Attest: "slsa"})
	if err != nil {
		t.Fatal(err)
	}
	if err := node.verifySource(context.Background(), fs, root, signed.ManifestDigest); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyOptionsMapsSigstorePolicy(t *testing.T) {
	fs := testFileSystem("agent-sigstore-policy")
	fs.Spec.Signing = &k8soperator.SigningSpec{
		CertificateIdentity:          "https://github.com/smol-platform/smol-agent-oci-fs/.github/workflows/release.yml@refs/heads/main",
		CertificateIdentityRegexp:    "^https://github.com/smol-platform/",
		CertificateOIDCIssuer:        "https://token.actions.githubusercontent.com",
		CertificateOIDCIssuerRegexp:  "^https://token\\.actions\\.githubusercontent\\.com$",
		SigstoreTrustedRoot:          "/var/run/osix/sigstore/trusted-root.json",
		SigstoreIgnoreTlog:           true,
		SigstoreIgnoreTimestamp:      true,
		SigstoreIgnoreCertificateSCT: true,
	}
	opts, cleanup, err := (Node{}).verifyOptions(context.Background(), fs)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if opts.CertificateIdentity != fs.Spec.Signing.CertificateIdentity ||
		opts.CertificateIdentityRegexp != fs.Spec.Signing.CertificateIdentityRegexp ||
		opts.CertificateOIDCIssuer != fs.Spec.Signing.CertificateOIDCIssuer ||
		opts.CertificateOIDCIssuerRegexp != fs.Spec.Signing.CertificateOIDCIssuerRegexp ||
		opts.SigstoreTrustedRoot != fs.Spec.Signing.SigstoreTrustedRoot {
		t.Fatalf("sigstore policy not mapped: %#v", opts)
	}
	if !opts.SigstoreIgnoreTlog || !opts.SigstoreIgnoreTimestamp || !opts.SigstoreIgnoreCertificateSCT {
		t.Fatalf("sigstore verifier toggles not mapped: %#v", opts)
	}
}

type fakeSecretProvider struct {
	data map[string][]byte
	err  error
}

func (p fakeSecretProvider) SecretData(ctx context.Context, namespace, name string) (map[string][]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	out := map[string][]byte{}
	for key, value := range p.data {
		out[key] = append([]byte(nil), value...)
	}
	return out, nil
}
