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

const (
	kmsEnvelopePrefix   = "OSIX-KMS-ENVELOPE-v1\n"
	layerEnvelopePrefix = "OSIX-LAYER-ENVELOPE-v1\n"
	layerEnvelopeAAD    = "osix-layer-envelope-v1"
)

type kmsEnvelope struct {
	Recipient string `json:"recipient"`
	Nonce     string `json:"nonce"`
}

type layerEnvelope struct {
	Version    string                 `json:"version"`
	Alg        string                 `json:"alg"`
	Nonce      string                 `json:"nonce"`
	Recipients []layerEnvelopeWrapKey `json:"recipients"`
}

type layerEnvelopeWrapKey struct {
	Type       string `json:"type"`
	Provider   string `json:"provider,omitempty"`
	Recipient  string `json:"recipient"`
	Alg        string `json:"alg"`
	Nonce      string `json:"nonce,omitempty"`
	WrappedKey string `json:"wrappedKey"`
}

type recipientSpec struct {
	Kind      string
	Provider  string
	Recipient string
}

type decryptMaterial struct {
	ageIdentities []age.Identity
	recipients    map[string]map[string]bool
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
	specs, err := parseRecipientSpecs(parts)
	if err != nil {
		return nil, nil, err
	}
	if len(specs) == 1 && specs[0].Kind == "kms" && specs[0].Provider == "aws" && strings.HasPrefix(specs[0].Recipient, "kms:aws:kms:") && !kmsProviderEnabled() {
		out, err := encryptKMS(data, specs[0].Recipient)
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
	if allAgeRecipients(specs) {
		return encryptAge(data, parts)
	}
	out, err := encryptEnvelope(data, specs)
	if err != nil {
		return nil, nil, err
	}
	return out, map[string]string{
		"com.osix.encryption.alg":             "aes-256-gcm",
		"com.osix.encryption.keywrap":         "osix-envelope",
		"com.osix.encryption.recipients":      fmt.Sprintf("%d", len(specs)),
		"com.osix.encryption.recipient_types": recipientTypes(specs),
		"com.osix.plaintext.digest":           digestBytes(data),
		"com.osix.plaintext.size":             fmt.Sprintf("%d", len(data)),
	}, nil
}

func encryptAge(data []byte, parts []string) ([]byte, map[string]string, error) {
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
	case "osix-envelope":
		plaintext, err = decryptEnvelope(data, decrypt)
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

func parseRecipientSpecs(parts []string) ([]recipientSpec, error) {
	specs := make([]recipientSpec, 0, len(parts))
	for _, part := range parts {
		spec, err := parseRecipientSpec(part)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func parseRecipientSpec(part string) (recipientSpec, error) {
	part = strings.TrimSpace(part)
	switch {
	case strings.HasPrefix(part, "age:"):
		recipient := strings.TrimPrefix(part, "age:")
		if recipient == "" {
			return recipientSpec{}, fmt.Errorf("empty age recipient")
		}
		return recipientSpec{Kind: "age", Provider: "age", Recipient: recipient}, nil
	case strings.HasPrefix(part, "age1"):
		return recipientSpec{Kind: "age", Provider: "age", Recipient: part}, nil
	case strings.HasPrefix(part, "kms:"):
		provider := "generic"
		rest := strings.TrimPrefix(part, "kms:")
		if before, _, ok := strings.Cut(rest, ":"); ok && before != "" {
			provider = before
		}
		return recipientSpec{Kind: "kms", Provider: provider, Recipient: part}, nil
	case strings.HasPrefix(part, "gpg:"):
		if strings.TrimPrefix(part, "gpg:") == "" {
			return recipientSpec{}, fmt.Errorf("empty gpg recipient")
		}
		return recipientSpec{Kind: "gpg", Provider: "gpg", Recipient: part}, nil
	case strings.HasPrefix(part, "endpoint:"):
		if strings.TrimPrefix(part, "endpoint:") == "" {
			return recipientSpec{}, fmt.Errorf("empty endpoint recipient")
		}
		return recipientSpec{Kind: "endpoint", Provider: "endpoint", Recipient: part}, nil
	case strings.HasPrefix(part, "https://") || strings.HasPrefix(part, "http://"):
		return recipientSpec{Kind: "endpoint", Provider: "url", Recipient: part}, nil
	default:
		return recipientSpec{}, fmt.Errorf("unsupported encryption recipient %q", part)
	}
}

func allAgeRecipients(specs []recipientSpec) bool {
	for _, spec := range specs {
		if spec.Kind != "age" {
			return false
		}
	}
	return len(specs) > 0
}

func recipientTypes(specs []recipientSpec) string {
	var types []string
	seen := map[string]bool{}
	for _, spec := range specs {
		if !seen[spec.Kind] {
			types = append(types, spec.Kind)
			seen[spec.Kind] = true
		}
	}
	return strings.Join(types, ",")
}

func encryptEnvelope(data []byte, specs []recipientSpec) ([]byte, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(dek)
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
	ciphertext := gcm.Seal(nil, nonce, data, []byte(layerEnvelopeAAD))
	header := layerEnvelope{
		Version:    "1",
		Alg:        "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Recipients: make([]layerEnvelopeWrapKey, 0, len(specs)),
	}
	for _, spec := range specs {
		wrap, err := wrapEnvelopeKey(dek, spec)
		if err != nil {
			return nil, err
		}
		header.Recipients = append(header.Recipients, wrap)
	}
	headerData, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	return append(append([]byte(layerEnvelopePrefix), headerData...), append([]byte("\n"), ciphertext...)...), nil
}

func wrapEnvelopeKey(dek []byte, spec recipientSpec) (layerEnvelopeWrapKey, error) {
	if spec.Kind == "age" {
		recipient, err := age.ParseX25519Recipient(spec.Recipient)
		if err != nil {
			return layerEnvelopeWrapKey{}, fmt.Errorf("parse age recipient: %w", err)
		}
		var buf bytes.Buffer
		w, err := age.Encrypt(&buf, recipient)
		if err != nil {
			return layerEnvelopeWrapKey{}, err
		}
		if _, err := w.Write(dek); err != nil {
			w.Close()
			return layerEnvelopeWrapKey{}, err
		}
		if err := w.Close(); err != nil {
			return layerEnvelopeWrapKey{}, err
		}
		return layerEnvelopeWrapKey{
			Type:       spec.Kind,
			Provider:   spec.Provider,
			Recipient:  spec.Recipient,
			Alg:        "age-v1",
			WrappedKey: base64.StdEncoding.EncodeToString(buf.Bytes()),
		}, nil
	}
	if wrap, ok, err := wrapProviderEnvelopeKey(dek, spec); ok || err != nil {
		return wrap, err
	}
	block, err := aes.NewCipher(localRecipientKey(spec.Kind, spec.Recipient))
	if err != nil {
		return layerEnvelopeWrapKey{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return layerEnvelopeWrapKey{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return layerEnvelopeWrapKey{}, err
	}
	wrapped := gcm.Seal(nil, nonce, dek, localWrapAAD(spec.Kind, spec.Recipient))
	return layerEnvelopeWrapKey{
		Type:       spec.Kind,
		Provider:   spec.Provider,
		Recipient:  spec.Recipient,
		Alg:        "aes-256-gcm-local-wrap",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		WrappedKey: base64.StdEncoding.EncodeToString(wrapped),
	}, nil
}

func decryptEnvelope(data []byte, decrypt string) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(layerEnvelopePrefix)) {
		return nil, fmt.Errorf("invalid layer envelope")
	}
	rest := bytes.TrimPrefix(data, []byte(layerEnvelopePrefix))
	headerData, ciphertext, ok := bytes.Cut(rest, []byte("\n"))
	if !ok {
		return nil, fmt.Errorf("invalid layer envelope header")
	}
	var header layerEnvelope
	if err := json.Unmarshal(headerData, &header); err != nil {
		return nil, err
	}
	if header.Version != "1" || header.Alg != "aes-256-gcm" {
		return nil, fmt.Errorf("unsupported layer envelope version %q alg %q", header.Version, header.Alg)
	}
	nonce, err := base64.StdEncoding.DecodeString(header.Nonce)
	if err != nil {
		return nil, err
	}
	material, err := parseDecryptMaterial(decrypt)
	if err != nil {
		return nil, err
	}
	for _, wrap := range header.Recipients {
		dek, ok, err := unwrapEnvelopeKey(wrap, material)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		block, err := aes.NewCipher(dek)
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		return gcm.Open(nil, nonce, ciphertext, []byte(layerEnvelopeAAD))
	}
	return nil, fmt.Errorf("no matching decrypt material provided for encrypted layer recipients %q", envelopeRecipientTypes(header.Recipients))
}

func unwrapEnvelopeKey(wrap layerEnvelopeWrapKey, material decryptMaterial) ([]byte, bool, error) {
	switch wrap.Type {
	case "age":
		if len(material.ageIdentities) == 0 {
			return nil, false, nil
		}
		dek, err := unwrapAgeEnvelopeKey(wrap, material.ageIdentities)
		if err != nil {
			return nil, false, nil
		}
		return dek, true, nil
	case "kms", "gpg", "endpoint":
		if !material.hasRecipient(wrap.Type, wrap.Recipient) {
			return nil, false, nil
		}
		if dek, ok, err := unwrapProviderEnvelopeKey(wrap); ok || err != nil {
			return dek, ok, err
		}
		dek, err := unwrapLocalEnvelopeKey(wrap)
		if err != nil {
			return nil, false, err
		}
		return dek, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported envelope recipient type %q", wrap.Type)
	}
}

func unwrapAgeEnvelopeKey(wrap layerEnvelopeWrapKey, identities []age.Identity) ([]byte, error) {
	wrapped, err := base64.StdEncoding.DecodeString(wrap.WrappedKey)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(wrapped), identities...)
	if err != nil {
		return nil, err
	}
	dek, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(dek) != 32 {
		return nil, fmt.Errorf("invalid envelope key length %d", len(dek))
	}
	return dek, nil
}

func unwrapLocalEnvelopeKey(wrap layerEnvelopeWrapKey) ([]byte, error) {
	nonce, err := base64.StdEncoding.DecodeString(wrap.Nonce)
	if err != nil {
		return nil, err
	}
	wrapped, err := base64.StdEncoding.DecodeString(wrap.WrappedKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(localRecipientKey(wrap.Type, wrap.Recipient))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	dek, err := gcm.Open(nil, nonce, wrapped, localWrapAAD(wrap.Type, wrap.Recipient))
	if err != nil {
		return nil, err
	}
	if len(dek) != 32 {
		return nil, fmt.Errorf("invalid envelope key length %d", len(dek))
	}
	return dek, nil
}

func parseDecryptMaterial(decrypt string) (decryptMaterial, error) {
	material := decryptMaterial{recipients: map[string]map[string]bool{}}
	for _, part := range splitCSV(decrypt) {
		spec, err := parseRecipientSpec(part)
		if err == nil && spec.Kind != "age" {
			material.addRecipient(spec.Kind, spec.Recipient)
			continue
		}
		identities, err := parseAgeIdentities(part)
		if err != nil {
			return decryptMaterial{}, err
		}
		material.ageIdentities = append(material.ageIdentities, identities...)
	}
	return material, nil
}

func (m decryptMaterial) addRecipient(kind, recipient string) {
	if m.recipients[kind] == nil {
		m.recipients[kind] = map[string]bool{}
	}
	m.recipients[kind][recipient] = true
}

func (m decryptMaterial) hasRecipient(kind, recipient string) bool {
	return m.recipients[kind] != nil && m.recipients[kind][recipient]
}

func envelopeRecipientTypes(wraps []layerEnvelopeWrapKey) string {
	seen := map[string]bool{}
	var types []string
	for _, wrap := range wraps {
		if !seen[wrap.Type] {
			types = append(types, wrap.Type)
			seen[wrap.Type] = true
		}
	}
	return strings.Join(types, ",")
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

func localRecipientKey(kind, recipient string) []byte {
	sum := sha256.Sum256([]byte("osix-local-" + kind + ":" + recipient))
	return sum[:]
}

func localWrapAAD(kind, recipient string) []byte {
	return []byte(kind + ":" + recipient)
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
