package osix

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
)

const kmsEnvelopePrefix = "OSIX-KMS-ENVELOPE-v1\n"

type kmsEnvelope struct {
	Recipient string `json:"recipient"`
	Nonce     string `json:"nonce"`
}

func encryptLayer(data []byte, recipients string) ([]byte, map[string]string, error) {
	recipients = strings.TrimSpace(recipients)
	if recipients == "" {
		return data, nil, nil
	}
	parts := splitCSV(recipients)
	if len(parts) == 0 {
		return data, nil, nil
	}
	first := parts[0]
	if strings.HasPrefix(first, "kms:aws:kms:") {
		if len(parts) != 1 {
			return nil, nil, fmt.Errorf("kms encryption currently accepts exactly one recipient")
		}
		out, err := encryptKMS(data, first)
		if err != nil {
			return nil, nil, err
		}
		return out, map[string]string{
			"com.osix.encryption.alg":        "aes-256-gcm",
			"com.osix.encryption.keywrap":    "aws-kms",
			"com.osix.encryption.recipients": "1",
			"com.osix.plaintext.digest":      digestBytes(data),
			"com.osix.plaintext.size":        fmt.Sprintf("%d", len(data)),
		}, nil
	}
	var ageRecipients []age.Recipient
	for _, part := range parts {
		part = strings.TrimPrefix(part, "age:")
		recipient, err := age.ParseX25519Recipient(part)
		if err != nil {
			return nil, nil, fmt.Errorf("parse age recipient: %w", err)
		}
		ageRecipients = append(ageRecipients, recipient)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, ageRecipients...)
	if err != nil {
		return nil, nil, err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, nil, err
	}
	if err := w.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), map[string]string{
		"com.osix.encryption.alg":        "age-v1",
		"com.osix.encryption.keywrap":    "age",
		"com.osix.encryption.recipients": fmt.Sprintf("%d", len(ageRecipients)),
		"com.osix.plaintext.digest":      digestBytes(data),
		"com.osix.plaintext.size":        fmt.Sprintf("%d", len(data)),
	}, nil
}

func decryptLayer(data []byte, descriptor Descriptor, decrypt string) ([]byte, error) {
	if descriptor.MediaType != MediaTypeLayerEnc {
		return data, nil
	}
	keywrap := descriptor.Annotations["com.osix.encryption.keywrap"]
	var plaintext []byte
	var err error
	switch keywrap {
	case "age":
		plaintext, err = decryptAge(data, decrypt)
	case "aws-kms":
		plaintext, err = decryptKMS(data, decrypt)
	default:
		err = fmt.Errorf("unsupported encrypted layer keywrap %q", keywrap)
	}
	if err != nil {
		return nil, err
	}
	if want := descriptor.Annotations["com.osix.plaintext.digest"]; want != "" {
		if got := digestBytes(plaintext); got != want {
			return nil, fmt.Errorf("plaintext digest mismatch: want %s got %s", want, got)
		}
	}
	return plaintext, nil
}

func decryptAge(data []byte, decrypt string) ([]byte, error) {
	identities, err := parseAgeIdentities(decrypt)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(data), identities...)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func parseAgeIdentities(decrypt string) ([]age.Identity, error) {
	var identities []age.Identity
	for _, part := range splitCSV(decrypt) {
		part = strings.TrimPrefix(part, "age:")
		if data, err := os.ReadFile(part); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				id, err := age.ParseX25519Identity(line)
				if err != nil {
					return nil, err
				}
				identities = append(identities, id)
			}
			continue
		}
		id, err := age.ParseX25519Identity(part)
		if err != nil {
			return nil, err
		}
		identities = append(identities, id)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("no age identities provided for encrypted layer")
	}
	return identities, nil
}

func encryptKMS(data []byte, recipient string) ([]byte, error) {
	block, err := aes.NewCipher(kmsKey(recipient))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, data, []byte(recipient))
	header, err := json.Marshal(kmsEnvelope{Recipient: recipient, Nonce: base64.StdEncoding.EncodeToString(nonce)})
	if err != nil {
		return nil, err
	}
	return append(append([]byte(kmsEnvelopePrefix), header...), append([]byte("\n"), ciphertext...)...), nil
}

func decryptKMS(data []byte, decrypt string) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(kmsEnvelopePrefix)) {
		return nil, fmt.Errorf("invalid kms envelope")
	}
	rest := bytes.TrimPrefix(data, []byte(kmsEnvelopePrefix))
	headerData, ciphertext, ok := bytes.Cut(rest, []byte("\n"))
	if !ok {
		return nil, fmt.Errorf("invalid kms envelope header")
	}
	var header kmsEnvelope
	if err := json.Unmarshal(headerData, &header); err != nil {
		return nil, err
	}
	if !contains(splitCSV(decrypt), header.Recipient) {
		return nil, fmt.Errorf("kms recipient %s was not provided for decrypt", header.Recipient)
	}
	nonce, err := base64.StdEncoding.DecodeString(header.Nonce)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(kmsKey(header.Recipient))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, []byte(header.Recipient))
}

func kmsKey(recipient string) []byte {
	sum := sha256.Sum256([]byte("osix-local-kms:" + recipient))
	return sum[:]
}

func splitCSV(input string) []string {
	var out []string
	for _, part := range strings.Split(input, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
