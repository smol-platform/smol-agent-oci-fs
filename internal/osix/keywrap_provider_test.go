package osix

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderBackedEnvelopeKeyWraps(t *testing.T) {
	root := t.TempDir()
	kmsWrap := writeProviderTestScript(t, root, "kms-wrap", `#!/bin/sh
set -eu
[ "${OSIX_KEYWRAP_OPERATION}" = "wrap" ]
printf 'kms:'
cat
`)
	kmsUnwrap := writeProviderTestScript(t, root, "kms-unwrap", `#!/bin/sh
set -eu
[ "${OSIX_KEYWRAP_OPERATION}" = "unwrap" ]
dd bs=1 skip=4 2>/dev/null
`)
	gpg := writeProviderTestScript(t, root, "gpg", `#!/bin/sh
set -eu
case " $* " in
  *" --encrypt "*) printf 'gpg:'; cat ;;
  *" --decrypt "*) dd bs=1 skip=4 2>/dev/null ;;
  *) exit 64 ;;
esac
`)
	endpointWraps := 0
	endpointUnwraps := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected endpoint method %s", r.Method)
		}
		var req endpointKeyWrapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		switch req.Operation {
		case "wrap":
			endpointWraps++
			key, err := base64.StdEncoding.DecodeString(req.PlaintextKey)
			if err != nil {
				t.Fatal(err)
			}
			writeEndpointResponse(t, w, endpointKeyWrapResponse{
				WrappedKey: base64.StdEncoding.EncodeToString(append([]byte("endpoint:"), key...)),
				Alg:        keywrapEndpointAlg,
			})
		case "unwrap":
			endpointUnwraps++
			wrapped, err := base64.StdEncoding.DecodeString(req.WrappedKey)
			if err != nil {
				t.Fatal(err)
			}
			writeEndpointResponse(t, w, endpointKeyWrapResponse{
				PlaintextKey: base64.StdEncoding.EncodeToString(bytes.TrimPrefix(wrapped, []byte("endpoint:"))),
			})
		default:
			t.Fatalf("unexpected endpoint operation %s", req.Operation)
		}
	}))
	defer endpoint.Close()

	t.Setenv("OSIX_KMS_WRAP_COMMAND", kmsWrap)
	t.Setenv("OSIX_KMS_UNWRAP_COMMAND", kmsUnwrap)
	t.Setenv("OSIX_GPG_COMMAND", gpg)
	t.Setenv("OSIX_ENDPOINT_PROVIDER", "http")

	plaintext := []byte("provider backed encrypted layer")
	recipients := "kms:aws:kms:us-east-1:123456789012:key/demo,gpg:alice@example.com,endpoint:" + endpoint.URL
	encrypted, annotations, err := encryptLayer(plaintext, recipients)
	if err != nil {
		t.Fatal(err)
	}
	if annotations["com.osix.encryption.keywrap"] != "osix-envelope" {
		t.Fatalf("unexpected keywrap: %#v", annotations)
	}
	if bytes.Contains(encrypted, plaintext) {
		t.Fatalf("provider-backed encrypted layer leaked plaintext")
	}
	for name, decrypt := range map[string]string{
		"kms":      "kms:aws:kms:us-east-1:123456789012:key/demo",
		"gpg":      "gpg:alice@example.com",
		"endpoint": "endpoint:" + endpoint.URL,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decryptLayer(encrypted, Descriptor{MediaType: MediaTypeLayerEnc, Annotations: annotations}, decrypt)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatalf("unexpected plaintext: %q", got)
			}
		})
	}
	if endpointWraps != 1 || endpointUnwraps != 1 {
		t.Fatalf("unexpected endpoint calls: wraps=%d unwraps=%d", endpointWraps, endpointUnwraps)
	}
}

func TestSingleKMSProviderRecipientUsesEnvelope(t *testing.T) {
	root := t.TempDir()
	kmsWrap := writeProviderTestScript(t, root, "kms-wrap", `#!/bin/sh
set -eu
printf 'kms:'
cat
`)
	kmsUnwrap := writeProviderTestScript(t, root, "kms-unwrap", `#!/bin/sh
set -eu
dd bs=1 skip=4 2>/dev/null
`)
	t.Setenv("OSIX_KMS_WRAP_COMMAND", kmsWrap)
	t.Setenv("OSIX_KMS_UNWRAP_COMMAND", kmsUnwrap)

	recipient := "kms:aws:kms:us-east-1:123456789012:key/demo"
	plaintext := []byte("single kms provider layer")
	encrypted, annotations, err := encryptLayer(plaintext, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if annotations["com.osix.encryption.keywrap"] != "osix-envelope" {
		t.Fatalf("provider-backed kms should use osix-envelope, got %#v", annotations)
	}
	got, err := decryptLayer(encrypted, Descriptor{MediaType: MediaTypeLayerEnc, Annotations: annotations}, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("unexpected plaintext: %q", got)
	}
}

func TestAWSKMSCLIProviderEnvelope(t *testing.T) {
	root := t.TempDir()
	aws := writeProviderTestScript(t, root, "aws", `#!/bin/sh
set -eu
file=""
mode="${1:-} ${2:-}"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --plaintext|--ciphertext-blob)
      shift
      file="${1#fileb://}"
      ;;
  esac
  shift || true
done
case "$mode" in
  "kms encrypt")
    blob="$( (printf 'aws:'; cat "$file") | base64 | tr -d '\n' )"
    printf '{"CiphertextBlob":"%s"}\n' "$blob"
    ;;
  "kms decrypt")
    blob="$( dd if="$file" bs=1 skip=4 2>/dev/null | base64 | tr -d '\n' )"
    printf '{"Plaintext":"%s"}\n' "$blob"
    ;;
  *)
    exit 64
    ;;
esac
`)
	t.Setenv("OSIX_KMS_PROVIDER", "aws")
	t.Setenv("OSIX_AWS_COMMAND", aws)

	recipient := "kms:aws:kms:us-east-1:123456789012:key/demo"
	plaintext := []byte("aws kms cli provider layer")
	encrypted, annotations, err := encryptLayer(plaintext, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if annotations["com.osix.encryption.keywrap"] != "osix-envelope" {
		t.Fatalf("aws kms provider should use osix-envelope, got %#v", annotations)
	}
	got, err := decryptLayer(encrypted, Descriptor{MediaType: MediaTypeLayerEnc, Annotations: annotations}, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("unexpected plaintext: %q", got)
	}
}

func writeProviderTestScript(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeEndpointResponse(t *testing.T, w http.ResponseWriter, response endpointKeyWrapResponse) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Fatal(err)
	}
}
