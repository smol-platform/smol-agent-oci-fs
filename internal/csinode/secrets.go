package csinode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
)

type SecretProvider interface {
	SecretData(ctx context.Context, namespace, name string) (map[string][]byte, error)
}

type RegistryCredentials struct {
	Env          map[string]string
	DockerConfig []byte
}

var registryEnvMu sync.Mutex

func (n Node) withRegistryCredentials(ctx context.Context, fs k8soperator.AgentOCIFileSystem, fn func() error) error {
	creds, err := n.registryCredentials(ctx, fs)
	if err != nil {
		return err
	}
	if len(creds.Env) == 0 && len(creds.DockerConfig) == 0 {
		return fn()
	}
	registryEnvMu.Lock()
	defer registryEnvMu.Unlock()

	env := map[string]string{}
	for key, value := range creds.Env {
		if strings.TrimSpace(value) != "" {
			env[key] = value
		}
	}
	var dockerConfigDir string
	if len(creds.DockerConfig) > 0 {
		dockerConfigDir, err = os.MkdirTemp("", "osix-registry-auth-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dockerConfigDir)
		if err := os.WriteFile(filepath.Join(dockerConfigDir, "config.json"), creds.DockerConfig, 0o600); err != nil {
			return err
		}
		env["DOCKER_CONFIG"] = dockerConfigDir
	}
	return withTemporaryEnv(env, fn)
}

func (n Node) registryCredentials(ctx context.Context, fs k8soperator.AgentOCIFileSystem) (RegistryCredentials, error) {
	fs = k8soperator.NormalizeFileSystem(fs)
	if fs.Spec.RegistrySecretRef == nil || strings.TrimSpace(fs.Spec.RegistrySecretRef.Name) == "" {
		return RegistryCredentials{}, nil
	}
	provider, ok := n.secretProvider()
	if !ok {
		return RegistryCredentials{}, fmt.Errorf("registrySecretRef %q requires an in-cluster Kubernetes secret provider", fs.Spec.RegistrySecretRef.Name)
	}
	data, err := provider.SecretData(ctx, namespaceOrDefault(fs.ObjectMeta.Namespace), fs.Spec.RegistrySecretRef.Name)
	if err != nil {
		return RegistryCredentials{}, err
	}
	return registryCredentialsFromSecret(data), nil
}

func registryCredentialsFromSecret(data map[string][]byte) RegistryCredentials {
	creds := RegistryCredentials{Env: map[string]string{}}
	if token := secretString(data, "token", "accessToken", "identitytoken", "identityToken"); token != "" {
		creds.Env["OSIX_REGISTRY_TOKEN"] = token
	}
	if username := secretString(data, "username", "user"); username != "" {
		creds.Env["OSIX_REGISTRY_USERNAME"] = username
	}
	if password := secretString(data, "password", "passwd"); password != "" {
		creds.Env["OSIX_REGISTRY_PASSWORD"] = password
	}
	for _, key := range []string{".dockerconfigjson", "dockerconfigjson", "config.json", "dockerConfigJson"} {
		if cfg := data[key]; len(cfg) > 0 {
			creds.DockerConfig = append([]byte(nil), cfg...)
			break
		}
	}
	if len(creds.Env) == 0 {
		creds.Env = nil
	}
	return creds
}

func secretString(data map[string][]byte, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(string(data[key])); value != "" {
			return value
		}
	}
	return ""
}

func (n Node) secretProvider() (SecretProvider, bool) {
	if n.SecretProvider != nil {
		return n.SecretProvider, true
	}
	return NewInClusterSecretProvider()
}

func withTemporaryEnv(env map[string]string, fn func() error) error {
	type previous struct {
		value string
		ok    bool
	}
	prior := map[string]previous{}
	for key, value := range env {
		old, ok := os.LookupEnv(key)
		prior[key] = previous{value: old, ok: ok}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	defer func() {
		for key, old := range prior {
			if old.ok {
				_ = os.Setenv(key, old.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}()
	return fn()
}

type KubernetesSecretProvider struct {
	Host       string
	TokenPath  string
	CACertPath string
	Client     *http.Client
}

func NewInClusterSecretProvider() (*KubernetesSecretProvider, bool) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, false
	}
	return &KubernetesSecretProvider{
		Host:       "https://" + host + ":" + port,
		TokenPath:  "/var/run/secrets/kubernetes.io/serviceaccount/token",
		CACertPath: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
	}, true
}

func (p *KubernetesSecretProvider) SecretData(ctx context.Context, namespace, name string) (map[string][]byte, error) {
	namespace = namespaceOrDefault(namespace)
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("secret name is required")
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := p.requestJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name), nil, &secret); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for key, encoded := range secret.Data {
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode secret %s/%s key %s: %w", namespace, name, key, err)
		}
		out[key] = value
	}
	return out, nil
}

func (p *KubernetesSecretProvider) requestJSON(ctx context.Context, method, path string, value any, out any) error {
	var body io.Reader
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.Host, "/")+path, body)
	if err != nil {
		return err
	}
	if value != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token, err := os.ReadFile(p.TokenPath)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := p.Client
	if client == nil {
		client = kubernetesHTTPClient(p.CACertPath)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kubernetes %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func namespaceOrDefault(namespace string) string {
	if strings.TrimSpace(namespace) == "" {
		return "default"
	}
	return namespace
}
