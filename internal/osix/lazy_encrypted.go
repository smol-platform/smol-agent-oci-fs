package osix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const encryptedLazyChunkSize = 64 * 1024

type encryptedLazyIndexRecord struct {
	Version      string                   `json:"version"`
	Subject      string                   `json:"subject"`
	IndexDigest  string                   `json:"indexDigest"`
	IndexSize    int64                    `json:"indexSize"`
	IndexMedia   string                   `json:"indexMediaType"`
	IndexKeywrap string                   `json:"indexKeywrap,omitempty"`
	IndexMeta    map[string]string        `json:"indexAnnotations,omitempty"`
	Files        []encryptedLazyFileEntry `json:"files"`
	CreatedAt    time.Time                `json:"createdAt"`
	EnvelopeOnly bool                     `json:"envelopeOnly"`
}

type encryptedLazyFileEntry struct {
	Path            string                    `json:"path"`
	Type            string                    `json:"type"`
	Deleted         bool                      `json:"deleted,omitempty"`
	Digest          string                    `json:"digest,omitempty"`
	Size            int64                     `json:"size,omitempty"`
	MediaType       string                    `json:"mediaType,omitempty"`
	Keywrap         string                    `json:"keywrap,omitempty"`
	Annotations     map[string]string         `json:"annotations,omitempty"`
	PlaintextDigest string                    `json:"plaintextDigest,omitempty"`
	PlaintextSize   int64                     `json:"plaintextSize,omitempty"`
	ChunkSize       int64                     `json:"chunkSize,omitempty"`
	MerkleRoot      string                    `json:"merkleRoot,omitempty"`
	Chunks          []encryptedLazyChunkEntry `json:"chunks,omitempty"`
}

type encryptedLazyChunkEntry struct {
	Index           int               `json:"index"`
	Offset          int64             `json:"offset"`
	Digest          string            `json:"digest"`
	Size            int64             `json:"size"`
	MediaType       string            `json:"mediaType,omitempty"`
	Keywrap         string            `json:"keywrap,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	PlaintextDigest string            `json:"plaintextDigest"`
	PlaintextSize   int64             `json:"plaintextSize"`
}

func createEncryptedLazyIndex(s store, subjectDigest, root string, entries []TreeEntry, whiteouts []string, recipients string) error {
	record := encryptedLazyIndexRecord{
		Version:    "1",
		Subject:    subjectDigest,
		IndexMedia: MediaTypeLazyEncryptedIndex,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
	for _, target := range whiteouts {
		record.Files = append(record.Files, encryptedLazyFileEntry{Path: target, Type: "whiteout", Deleted: true})
	}
	for _, entry := range entries {
		if entry.Type != "file" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			return err
		}
		if shouldRedact(entry.Path) {
			data = redactLog(data)
		}
		encrypted, annotations, err := encryptLayer(data, recipients)
		if err != nil {
			return err
		}
		desc, err := s.writeBlob(encrypted)
		if err != nil {
			return err
		}
		chunks, merkleRoot, err := createEncryptedLazyChunks(s, data, recipients)
		if err != nil {
			return err
		}
		record.Files = append(record.Files, encryptedLazyFileEntry{
			Path:            entry.Path,
			Type:            "file",
			Digest:          desc.Digest,
			Size:            desc.Size,
			MediaType:       MediaTypeLazyEncryptedFile,
			Keywrap:         annotations["com.osix.encryption.keywrap"],
			Annotations:     annotations,
			PlaintextDigest: digestBytes(data),
			PlaintextSize:   int64(len(data)),
			ChunkSize:       encryptedLazyChunkSize,
			MerkleRoot:      merkleRoot,
			Chunks:          chunks,
		})
	}
	sort.Slice(record.Files, func(i, j int) bool {
		if record.Files[i].Path == record.Files[j].Path {
			return record.Files[i].Type < record.Files[j].Type
		}
		return record.Files[i].Path < record.Files[j].Path
	})
	if len(record.Files) == 0 {
		return nil
	}
	record.EnvelopeOnly = encryptedLazyFilesUseKeywrap(record.Files, "osix-envelope")
	indexPlaintext, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encryptedIndex, indexAnnotations, err := encryptLayer(indexPlaintext, recipients)
	if err != nil {
		return err
	}
	indexDesc, err := s.writeBlob(encryptedIndex)
	if err != nil {
		return err
	}
	record.IndexDigest = indexDesc.Digest
	record.IndexSize = indexDesc.Size
	record.IndexKeywrap = indexAnnotations["com.osix.encryption.keywrap"]
	record.IndexMeta = indexAnnotations
	record.EnvelopeOnly = record.EnvelopeOnly && record.IndexKeywrap == "osix-envelope"
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path, err := encryptedLazyRecordPath(s, subjectDigest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := writePrivateFile(path, data); err != nil {
		return err
	}
	return s.writeRef(encryptedLazyIndexRefName(subjectDigest), record.IndexDigest)
}

func encryptedLazyFilesUseKeywrap(entries []encryptedLazyFileEntry, keywrap string) bool {
	for _, entry := range entries {
		if entry.Type != "file" || entry.Deleted {
			continue
		}
		if entry.Keywrap != keywrap {
			return false
		}
	}
	return true
}

func createEncryptedLazyChunks(s store, data []byte, recipients string) ([]encryptedLazyChunkEntry, string, error) {
	var chunks []encryptedLazyChunkEntry
	var chunkDigests []string
	for offset, index := 0, 0; offset < len(data) || (len(data) == 0 && index == 0); index++ {
		end := offset + encryptedLazyChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		encrypted, annotations, err := encryptLayer(chunk, recipients)
		if err != nil {
			return nil, "", err
		}
		desc, err := s.writeBlob(encrypted)
		if err != nil {
			return nil, "", err
		}
		chunkDigest := digestBytes(chunk)
		chunkDigests = append(chunkDigests, chunkDigest)
		chunks = append(chunks, encryptedLazyChunkEntry{
			Index:           index,
			Offset:          int64(offset),
			Digest:          desc.Digest,
			Size:            desc.Size,
			MediaType:       MediaTypeLazyEncryptedFile,
			Keywrap:         annotations["com.osix.encryption.keywrap"],
			Annotations:     annotations,
			PlaintextDigest: chunkDigest,
			PlaintextSize:   int64(len(chunk)),
		})
		if end == len(data) {
			break
		}
		offset = end
	}
	return chunks, encryptedLazyMerkleRoot(chunkDigests), nil
}

func encryptedLazyMerkleRoot(chunkDigests []string) string {
	if len(chunkDigests) == 0 {
		return digestBytes(nil)
	}
	level := append([]string{}, chunkDigests...)
	for len(level) > 1 {
		var next []string
		for i := 0; i < len(level); i += 2 {
			left := level[i]
			right := left
			if i+1 < len(level) {
				right = level[i+1]
			}
			next = append(next, digestBytes([]byte(left+"\n"+right)))
		}
		level = next
	}
	return level[0]
}

func readEncryptedLazyChunks(s store, entry encryptedLazyFileEntry, offset, length int64, decrypt string) ([]byte, error) {
	if length == 0 {
		return []byte{}, nil
	}
	if offset < 0 || length < 0 {
		return nil, fmt.Errorf("invalid encrypted lazy chunk range offset=%d length=%d", offset, length)
	}
	end := offset + length
	var out bytes.Buffer
	var chunkDigests []string
	for _, chunk := range entry.Chunks {
		chunkDigests = append(chunkDigests, chunk.PlaintextDigest)
		chunkStart := chunk.Offset
		chunkEnd := chunk.Offset + chunk.PlaintextSize
		if chunkEnd <= offset || chunkStart >= end {
			continue
		}
		plaintext, err := readEncryptedLazyChunk(s, chunk, decrypt)
		if err != nil {
			return nil, err
		}
		startInChunk := int64(0)
		if offset > chunkStart {
			startInChunk = offset - chunkStart
		}
		endInChunk := int64(len(plaintext))
		if end < chunkEnd {
			endInChunk = end - chunkStart
		}
		if startInChunk < 0 || endInChunk < startInChunk || endInChunk > int64(len(plaintext)) {
			return nil, fmt.Errorf("invalid encrypted lazy chunk slice for %s chunk %d", entry.Path, chunk.Index)
		}
		if _, err := out.Write(plaintext[startInChunk:endInChunk]); err != nil {
			return nil, err
		}
	}
	if offset == 0 && length == entry.PlaintextSize && entry.MerkleRoot != "" {
		if got := encryptedLazyMerkleRoot(chunkDigests); got != entry.MerkleRoot {
			return nil, fmt.Errorf("encrypted lazy merkle root mismatch for %s: want %s got %s", entry.Path, entry.MerkleRoot, got)
		}
	}
	return out.Bytes(), nil
}

func readEncryptedLazyChunk(s store, chunk encryptedLazyChunkEntry, decrypt string) ([]byte, error) {
	encrypted, err := s.readBlob(chunk.Digest)
	if err != nil {
		if fetchErr := fetchRemoteBlobFromSource(s, chunk.Digest); fetchErr != nil {
			return nil, err
		}
		encrypted, err = s.readBlob(chunk.Digest)
		if err != nil {
			return nil, err
		}
	}
	plaintext, err := decryptEncryptedLazyBlob(encrypted, chunk.lazyDescriptor(), decrypt)
	if err != nil {
		return nil, err
	}
	if got := digestBytes(plaintext); got != chunk.PlaintextDigest {
		return nil, fmt.Errorf("encrypted lazy chunk digest mismatch for chunk %d: want %s got %s", chunk.Index, chunk.PlaintextDigest, got)
	}
	if int64(len(plaintext)) != chunk.PlaintextSize {
		return nil, fmt.Errorf("encrypted lazy chunk size mismatch for chunk %d: want %d got %d", chunk.Index, chunk.PlaintextSize, len(plaintext))
	}
	return plaintext, nil
}

func readEncryptedLazyFile(s store, subjectDigest, name, decrypt string) ([]byte, bool, bool, error) {
	if err := ensureEncryptedLazyIndexRecord(s, subjectDigest, decrypt); err != nil {
		return nil, false, false, err
	}
	path, err := encryptedLazyRecordPath(s, subjectDigest)
	if err != nil {
		return nil, false, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	var record encryptedLazyIndexRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, false, false, fmt.Errorf("parse encrypted lazy index record: %w", err)
	}
	if record.Subject != subjectDigest {
		return nil, false, false, fmt.Errorf("encrypted lazy index subject mismatch: %s != %s", record.Subject, subjectDigest)
	}
	for _, entry := range record.Files {
		if entry.Path != name {
			continue
		}
		if entry.Deleted {
			return nil, false, true, nil
		}
		if entry.Type != "file" {
			return nil, false, false, nil
		}
		if len(entry.Chunks) > 1 {
			plaintext, err := readEncryptedLazyChunks(s, entry, 0, entry.PlaintextSize, decrypt)
			if err != nil {
				return nil, false, false, err
			}
			return plaintext, true, false, nil
		}
		encrypted, err := s.readBlob(entry.Digest)
		if err != nil {
			if fetchErr := fetchRemoteBlobFromSource(s, entry.Digest); fetchErr != nil {
				return nil, false, false, err
			}
			encrypted, err = s.readBlob(entry.Digest)
			if err != nil {
				return nil, false, false, err
			}
		}
		plaintext, err := decryptEncryptedLazyBlob(encrypted, entry.lazyDescriptor(), decrypt)
		if err != nil {
			return nil, false, false, err
		}
		if entry.PlaintextDigest != "" {
			if got := digestBytes(plaintext); got != entry.PlaintextDigest {
				return nil, false, false, fmt.Errorf("encrypted lazy plaintext digest mismatch for %s: want %s got %s", entry.Path, entry.PlaintextDigest, got)
			}
		}
		if entry.PlaintextSize > 0 && int64(len(plaintext)) != entry.PlaintextSize {
			return nil, false, false, fmt.Errorf("encrypted lazy plaintext size mismatch for %s: want %d got %d", entry.Path, entry.PlaintextSize, len(plaintext))
		}
		return plaintext, true, false, nil
	}
	return nil, false, false, nil
}

func readEncryptedLazyFileRange(s store, subjectDigest, name, decrypt string, offset, length int64) ([]byte, bool, bool, error) {
	if offset < 0 {
		return nil, false, false, fmt.Errorf("range offset must be non-negative")
	}
	if length < 0 {
		return nil, false, false, fmt.Errorf("range length must be non-negative")
	}
	if err := ensureEncryptedLazyIndexRecord(s, subjectDigest, decrypt); err != nil {
		return nil, false, false, err
	}
	path, err := encryptedLazyRecordPath(s, subjectDigest)
	if err != nil {
		return nil, false, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	var record encryptedLazyIndexRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, false, false, fmt.Errorf("parse encrypted lazy index record: %w", err)
	}
	if record.Subject != subjectDigest {
		return nil, false, false, fmt.Errorf("encrypted lazy index subject mismatch: %s != %s", record.Subject, subjectDigest)
	}
	for _, entry := range record.Files {
		if entry.Path != name {
			continue
		}
		if entry.Deleted {
			return nil, false, true, nil
		}
		if entry.Type != "file" {
			return nil, false, false, nil
		}
		if len(entry.Chunks) > 0 {
			available := entry.PlaintextSize - offset
			if available < 0 {
				available = 0
			}
			if length > available {
				length = available
			}
			plaintext, err := readEncryptedLazyChunks(s, entry, offset, length, decrypt)
			if err != nil {
				return nil, false, false, err
			}
			return plaintext, true, false, nil
		}
		plaintext, found, deleted, err := readEncryptedLazyFile(s, subjectDigest, name, decrypt)
		if err != nil || !found || deleted {
			return plaintext, found, deleted, err
		}
		return sliceRange(plaintext, offset, length), true, false, nil
	}
	return nil, false, false, nil
}

func encryptedLazyRecordPath(s store, subjectDigest string) (string, error) {
	hexDigest, err := digestHex(subjectDigest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.lazyRoot(), hexDigest+".json"), nil
}

func ensureEncryptedLazyIndexRecord(s store, subjectDigest, decrypt string) error {
	path, err := encryptedLazyRecordPath(s, subjectDigest)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	indexDigest, err := s.resolveRef(encryptedLazyIndexRefName(subjectDigest))
	if err != nil {
		return nil
	}
	encryptedIndex, err := s.readBlob(indexDigest)
	if err != nil {
		if fetchErr := fetchRemoteBlobFromSource(s, indexDigest); fetchErr != nil {
			return err
		}
		encryptedIndex, err = s.readBlob(indexDigest)
		if err != nil {
			return err
		}
	}
	plaintext, err := decryptEncryptedLazyBlob(encryptedIndex, encryptedLazyIndexDescriptor(indexDigest, int64(len(encryptedIndex))), decrypt)
	if err != nil {
		return err
	}
	var record encryptedLazyIndexRecord
	if err := json.Unmarshal(plaintext, &record); err != nil {
		return fmt.Errorf("parse encrypted lazy index: %w", err)
	}
	if record.Subject != subjectDigest {
		return fmt.Errorf("encrypted lazy index subject mismatch: %s != %s", record.Subject, subjectDigest)
	}
	record.IndexDigest = indexDigest
	record.IndexSize = int64(len(encryptedIndex))
	if record.IndexMedia == "" {
		record.IndexMedia = MediaTypeLazyEncryptedIndex
	}
	if source, err := s.readRemoteBlobSource(indexDigest); err == nil {
		for _, entry := range record.Files {
			if entry.Digest != "" {
				_ = s.writeRemoteBlobSource(remoteBlobSource{Registry: source.Registry, Repo: source.Repo, Digest: entry.Digest})
			}
			for _, chunk := range entry.Chunks {
				if chunk.Digest != "" {
					_ = s.writeRemoteBlobSource(remoteBlobSource{Registry: source.Registry, Repo: source.Repo, Digest: chunk.Digest})
				}
			}
		}
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

func decryptEncryptedLazyBlob(encrypted []byte, desc Descriptor, decrypt string) ([]byte, error) {
	if desc.MediaType == "" {
		desc.MediaType = MediaTypeLayerEnc
	}
	if desc.Annotations == nil {
		desc.Annotations = map[string]string{}
	}
	keywrap := desc.Annotations["com.osix.encryption.keywrap"]
	if keywrap == "" || (keywrap == "osix-envelope" && !bytes.HasPrefix(encrypted, []byte(layerEnvelopePrefix))) {
		desc.Annotations["com.osix.encryption.keywrap"] = inferEncryptedLazyKeywrap(encrypted)
	}
	return decryptLayer(encrypted, desc, decrypt)
}

func inferEncryptedLazyKeywrap(encrypted []byte) string {
	switch {
	case bytes.HasPrefix(encrypted, []byte(layerEnvelopePrefix)):
		return "osix-envelope"
	case bytes.HasPrefix(encrypted, []byte(kmsEnvelopePrefix)):
		return "aws-kms"
	default:
		return "age"
	}
}

func (entry encryptedLazyFileEntry) lazyDescriptor() Descriptor {
	annotations := entry.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	if entry.Keywrap != "" && annotations["com.osix.encryption.keywrap"] == "" {
		annotations["com.osix.encryption.keywrap"] = entry.Keywrap
	}
	if entry.PlaintextDigest != "" && annotations["com.osix.plaintext.digest"] == "" {
		annotations["com.osix.plaintext.digest"] = entry.PlaintextDigest
	}
	if entry.PlaintextSize > 0 && annotations["com.osix.plaintext.size"] == "" {
		annotations["com.osix.plaintext.size"] = strconv.FormatInt(entry.PlaintextSize, 10)
	}
	return Descriptor{
		MediaType:   MediaTypeLayerEnc,
		Digest:      entry.Digest,
		Size:        entry.Size,
		Annotations: annotations,
	}
}

func (chunk encryptedLazyChunkEntry) lazyDescriptor() Descriptor {
	annotations := chunk.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	if chunk.Keywrap != "" && annotations["com.osix.encryption.keywrap"] == "" {
		annotations["com.osix.encryption.keywrap"] = chunk.Keywrap
	}
	if chunk.PlaintextDigest != "" && annotations["com.osix.plaintext.digest"] == "" {
		annotations["com.osix.plaintext.digest"] = chunk.PlaintextDigest
	}
	if chunk.PlaintextSize > 0 && annotations["com.osix.plaintext.size"] == "" {
		annotations["com.osix.plaintext.size"] = strconv.FormatInt(chunk.PlaintextSize, 10)
	}
	return Descriptor{
		MediaType:   MediaTypeLayerEnc,
		Digest:      chunk.Digest,
		Size:        chunk.Size,
		Annotations: annotations,
	}
}

func encryptedLazyIndexDescriptor(digest string, size int64) Descriptor {
	return Descriptor{
		MediaType: MediaTypeLayerEnc,
		Digest:    digest,
		Size:      size,
		Annotations: map[string]string{
			"com.osix.encryption.keywrap": "osix-envelope",
		},
	}
}

func readEncryptedLazyIndexRecord(s store, subjectDigest string) (encryptedLazyIndexRecord, error) {
	path, err := encryptedLazyRecordPath(s, subjectDigest)
	if err != nil {
		return encryptedLazyIndexRecord{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return encryptedLazyIndexRecord{}, err
	}
	var record encryptedLazyIndexRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return encryptedLazyIndexRecord{}, fmt.Errorf("parse encrypted lazy index record: %w", err)
	}
	if record.Subject != subjectDigest {
		return encryptedLazyIndexRecord{}, fmt.Errorf("encrypted lazy index subject mismatch: %s != %s", record.Subject, subjectDigest)
	}
	return record, nil
}

func encryptedLazyIndexRefName(digest string) string {
	return "lazy-encrypted-index-" + strings.TrimPrefix(digest, "sha256:")
}

func encryptedLazyIndexTag(digest string) string {
	return strings.ReplaceAll(digest, ":", "-") + ".osix-lazy"
}
