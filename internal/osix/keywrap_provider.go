package osix

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	keywrapKMSCommandAlg = "kms-command-v1"
	keywrapAWSKMSAlg     = "aws-kms-cli-v1"
	keywrapGPGAlg        = "gpg-cli-v1"
	keywrapEndpointAlg   = "endpoint-http-v1"
)

type endpointKeyWrapRequest struct {
	Operation    string `json:"operation"`
	Recipient    string `json:"recipient"`
	PlaintextKey string `json:"plaintextKey,omitempty"`
	WrappedKey   string `json:"wrappedKey,omitempty"`
}

type endpointKeyWrapResponse struct {
	PlaintextKey string `json:"plaintextKey,omitempty"`
	WrappedKey   string `json:"wrappedKey,omitempty"`
	Alg          string `json:"alg,omitempty"`
}

func kmsProviderEnabled() bool {
	return strings.TrimSpace(os.Getenv("OSIX_KMS_PROVIDER")) != "" ||
		strings.TrimSpace(os.Getenv("OSIX_KMS_WRAP_COMMAND")) != ""
}

func wrapProviderEnvelopeKey(dek []byte, spec recipientSpec) (layerEnvelopeWrapKey, bool, error) {
	switch spec.Kind {
	case "kms":
		if !kmsProviderEnabled() {
			return layerEnvelopeWrapKey{}, false, nil
		}
		wrapped, alg, err := wrapKMSProviderKey(dek, spec)
		return providerWrapKey(spec, alg, wrapped), true, err
	case "gpg":
		if !gpgProviderEnabled() {
			return layerEnvelopeWrapKey{}, false, nil
		}
		wrapped, err := wrapGPGProviderKey(dek, spec)
		return providerWrapKey(spec, keywrapGPGAlg, wrapped), true, err
	case "endpoint":
		if !endpointProviderEnabled() {
			return layerEnvelopeWrapKey{}, false, nil
		}
		wrapped, err := wrapEndpointProviderKey(dek, spec)
		return providerWrapKey(spec, keywrapEndpointAlg, wrapped), true, err
	default:
		return layerEnvelopeWrapKey{}, false, nil
	}
}

func unwrapProviderEnvelopeKey(wrap layerEnvelopeWrapKey) ([]byte, bool, error) {
	wrapped, err := base64.StdEncoding.DecodeString(wrap.WrappedKey)
	if err != nil {
		return nil, true, err
	}
	var dek []byte
	switch wrap.Alg {
	case keywrapKMSCommandAlg:
		dek, err = unwrapKMSCommandKey(wrapped, wrap.Recipient)
	case keywrapAWSKMSAlg:
		dek, err = unwrapAWSKMSKey(wrapped, wrap.Recipient)
	case keywrapGPGAlg:
		dek, err = unwrapGPGProviderKey(wrapped, wrap.Recipient)
	case keywrapEndpointAlg:
		dek, err = unwrapEndpointProviderKey(wrapped, wrap.Recipient)
	case "", "aes-256-gcm-local-wrap":
		return nil, false, nil
	default:
		return nil, true, fmt.Errorf("unsupported provider keywrap alg %q", wrap.Alg)
	}
	if err != nil {
		return nil, true, err
	}
	if len(dek) != 32 {
		return nil, true, fmt.Errorf("invalid provider envelope key length %d", len(dek))
	}
	return dek, true, nil
}

func providerWrapKey(spec recipientSpec, alg string, wrapped []byte) layerEnvelopeWrapKey {
	return layerEnvelopeWrapKey{
		Type:       spec.Kind,
		Provider:   spec.Provider,
		Recipient:  spec.Recipient,
		Alg:        alg,
		WrappedKey: base64.StdEncoding.EncodeToString(wrapped),
	}
}

func wrapKMSProviderKey(dek []byte, spec recipientSpec) ([]byte, string, error) {
	if command := strings.TrimSpace(os.Getenv("OSIX_KMS_WRAP_COMMAND")); command != "" {
		out, err := runProviderCommand(command, dek, providerEnv("wrap", spec))
		return out, keywrapKMSCommandAlg, err
	}
	if strings.TrimSpace(os.Getenv("OSIX_KMS_PROVIDER")) != "aws" {
		return nil, "", fmt.Errorf("unsupported KMS provider %q", os.Getenv("OSIX_KMS_PROVIDER"))
	}
	out, err := wrapAWSKMSKey(dek, spec.Recipient)
	return out, keywrapAWSKMSAlg, err
}

func unwrapKMSCommandKey(wrapped []byte, recipient string) ([]byte, error) {
	command := strings.TrimSpace(os.Getenv("OSIX_KMS_UNWRAP_COMMAND"))
	if command == "" {
		return nil, fmt.Errorf("OSIX_KMS_UNWRAP_COMMAND is required for %s", keywrapKMSCommandAlg)
	}
	return runProviderCommand(command, wrapped, providerEnv("unwrap", recipientSpec{Kind: "kms", Recipient: recipient}))
}

func wrapAWSKMSKey(dek []byte, recipient string) ([]byte, error) {
	region, keyID, err := awsKMSRecipient(recipient)
	if err != nil {
		return nil, err
	}
	plaintextFile, cleanup, err := writeTempKeyFile(dek)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	env := append(providerEnv("wrap", recipientSpec{Kind: "kms", Provider: "aws", Recipient: recipient}),
		"OSIX_AWS_KMS_REGION="+region,
		"OSIX_AWS_KMS_KEY_ID="+keyID,
	)
	out, err := runProviderCommand(awsCommand(), nil, env,
		"kms", "encrypt",
		"--region", region,
		"--key-id", keyID,
		"--plaintext", "fileb://"+plaintextFile,
		"--encryption-context", "osix=layer-envelope",
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	var response struct {
		CiphertextBlob string `json:"CiphertextBlob"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return nil, fmt.Errorf("parse aws kms encrypt response: %w", err)
	}
	if response.CiphertextBlob == "" {
		return nil, fmt.Errorf("aws kms encrypt response missing CiphertextBlob")
	}
	return base64.StdEncoding.DecodeString(response.CiphertextBlob)
}

func unwrapAWSKMSKey(wrapped []byte, recipient string) ([]byte, error) {
	region, keyID, err := awsKMSRecipient(recipient)
	if err != nil {
		return nil, err
	}
	ciphertextFile, cleanup, err := writeTempKeyFile(wrapped)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	env := append(providerEnv("unwrap", recipientSpec{Kind: "kms", Provider: "aws", Recipient: recipient}),
		"OSIX_AWS_KMS_REGION="+region,
		"OSIX_AWS_KMS_KEY_ID="+keyID,
	)
	out, err := runProviderCommand(awsCommand(), nil, env,
		"kms", "decrypt",
		"--region", region,
		"--ciphertext-blob", "fileb://"+ciphertextFile,
		"--encryption-context", "osix=layer-envelope",
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	var response struct {
		Plaintext string `json:"Plaintext"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return nil, fmt.Errorf("parse aws kms decrypt response: %w", err)
	}
	if response.Plaintext == "" {
		return nil, fmt.Errorf("aws kms decrypt response missing Plaintext")
	}
	return base64.StdEncoding.DecodeString(response.Plaintext)
}

func awsKMSRecipient(recipient string) (string, string, error) {
	const prefix = "kms:aws:kms:"
	if !strings.HasPrefix(recipient, prefix) {
		return "", "", fmt.Errorf("aws kms recipient must start with %q", prefix)
	}
	rest := strings.TrimPrefix(recipient, prefix)
	region, remainder, ok := strings.Cut(rest, ":")
	if !ok || region == "" || remainder == "" {
		return "", "", fmt.Errorf("invalid aws kms recipient %q", recipient)
	}
	if strings.HasPrefix(remainder, "arn:") {
		return region, remainder, nil
	}
	return region, "arn:aws:kms:" + region + ":" + remainder, nil
}

func awsCommand() string {
	if command := strings.TrimSpace(os.Getenv("OSIX_AWS_COMMAND")); command != "" {
		return command
	}
	return "aws"
}

func gpgProviderEnabled() bool {
	return strings.TrimSpace(os.Getenv("OSIX_GPG_PROVIDER")) == "gpg" ||
		strings.TrimSpace(os.Getenv("OSIX_GPG_COMMAND")) != ""
}

func wrapGPGProviderKey(dek []byte, spec recipientSpec) ([]byte, error) {
	recipient := strings.TrimPrefix(spec.Recipient, "gpg:")
	args := append(gpgBaseArgs(), "--encrypt", "--recipient", recipient, "--output", "-")
	return runProviderCommand(gpgCommand(), dek, providerEnv("wrap", spec), args...)
}

func unwrapGPGProviderKey(wrapped []byte, recipient string) ([]byte, error) {
	spec := recipientSpec{Kind: "gpg", Provider: "gpg", Recipient: recipient}
	args := append(gpgBaseArgs(), "--decrypt")
	return runProviderCommand(gpgCommand(), wrapped, providerEnv("unwrap", spec), args...)
}

func gpgCommand() string {
	if command := strings.TrimSpace(os.Getenv("OSIX_GPG_COMMAND")); command != "" {
		return command
	}
	return "gpg"
}

func gpgBaseArgs() []string {
	args := []string{"--batch", "--yes", "--trust-model", "always"}
	if home := strings.TrimSpace(os.Getenv("OSIX_GPG_HOMEDIR")); home != "" {
		args = append([]string{"--homedir", home}, args...)
	}
	return args
}

func endpointProviderEnabled() bool {
	return strings.TrimSpace(os.Getenv("OSIX_ENDPOINT_PROVIDER")) == "http"
}

func wrapEndpointProviderKey(dek []byte, spec recipientSpec) ([]byte, error) {
	response, err := callEndpointProvider(endpointURL(spec.Recipient), endpointKeyWrapRequest{
		Operation:    "wrap",
		Recipient:    spec.Recipient,
		PlaintextKey: base64.StdEncoding.EncodeToString(dek),
	})
	if err != nil {
		return nil, err
	}
	if response.WrappedKey == "" {
		return nil, fmt.Errorf("endpoint wrap response missing wrappedKey")
	}
	return base64.StdEncoding.DecodeString(response.WrappedKey)
}

func unwrapEndpointProviderKey(wrapped []byte, recipient string) ([]byte, error) {
	response, err := callEndpointProvider(endpointURL(recipient), endpointKeyWrapRequest{
		Operation:  "unwrap",
		Recipient:  recipient,
		WrappedKey: base64.StdEncoding.EncodeToString(wrapped),
	})
	if err != nil {
		return nil, err
	}
	if response.PlaintextKey == "" {
		return nil, fmt.Errorf("endpoint unwrap response missing plaintextKey")
	}
	return base64.StdEncoding.DecodeString(response.PlaintextKey)
}

func endpointURL(recipient string) string {
	return strings.TrimPrefix(recipient, "endpoint:")
}

func callEndpointProvider(url string, request endpointKeyWrapRequest) (endpointKeyWrapResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return endpointKeyWrapResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), providerTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return endpointKeyWrapResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(os.Getenv("OSIX_ENDPOINT_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return endpointKeyWrapResponse{}, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return endpointKeyWrapResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return endpointKeyWrapResponse{}, fmt.Errorf("endpoint keywrap %s returned %s", request.Operation, resp.Status)
	}
	var response endpointKeyWrapResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return endpointKeyWrapResponse{}, fmt.Errorf("parse endpoint keywrap response: %w", err)
	}
	return response, nil
}

func runProviderCommand(command string, input []byte, env []string, args ...string) ([]byte, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty provider command")
	}
	args = append(fields[1:], args...)
	ctx, cancel := context.WithTimeout(context.Background(), providerTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, fields[0], args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("provider command %s timed out", filepath.Base(fields[0]))
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("provider command %s failed: %s", filepath.Base(fields[0]), msg)
	}
	return out, nil
}

func providerEnv(operation string, spec recipientSpec) []string {
	return []string{
		"OSIX_KEYWRAP_OPERATION=" + operation,
		"OSIX_KEYWRAP_TYPE=" + spec.Kind,
		"OSIX_KEYWRAP_PROVIDER=" + spec.Provider,
		"OSIX_KEYWRAP_RECIPIENT=" + spec.Recipient,
	}
}

func providerTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("OSIX_KEYWRAP_TIMEOUT")); raw != "" {
		if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
			return duration
		}
	}
	return 30 * time.Second
}

func writeTempKeyFile(data []byte) (string, func(), error) {
	file, err := os.CreateTemp("", "osix-keywrap-*")
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", nil, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}
