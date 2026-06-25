package osix

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func ReadSnapshotFile(workspaceRoot, ref, name string, opts ReadFileOptions) ([]byte, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return nil, err
	}
	digest, err := s.resolveRef(ref)
	if err != nil {
		return nil, err
	}
	chain, err := s.snapshotChainWithDigests(digest)
	if err != nil {
		return nil, err
	}
	name, err = canonicalReadPath(name)
	if err != nil {
		return nil, err
	}
	for i := len(chain) - 1; i >= 0; i-- {
		item := chain[i]
		if len(item.Manifest.Layers) != 1 {
			return nil, fmt.Errorf("local prototype expects exactly one layer, got %d", len(item.Manifest.Layers))
		}
		layerDesc := item.Manifest.Layers[0]
		if layerDesc.MediaType == MediaTypeLayerEnc {
			if opts.Decrypt == "" {
				return nil, fmt.Errorf("lazy encrypted file reads require decrypt material")
			}
			content, found, deleted, err := readEncryptedLazyFile(s, item.Digest, name, opts.Decrypt)
			if err != nil {
				return nil, err
			}
			if deleted {
				return nil, fmt.Errorf("file %s was deleted by snapshot %s", name, item.Digest)
			}
			if found {
				return content, nil
			}
		}
		layer, err := s.readBlob(layerDesc.Digest)
		if err != nil {
			if fetchErr := fetchRemoteBlobFromSource(s, layerDesc.Digest); fetchErr != nil {
				return nil, err
			}
			layer, err = s.readBlob(layerDesc.Digest)
			if err != nil {
				return nil, err
			}
		}
		layer, err = decryptLayer(layer, layerDesc, opts.Decrypt)
		if err != nil {
			return nil, err
		}
		content, found, deleted, err := readFileFromLayer(layer, name)
		if err != nil {
			return nil, err
		}
		if deleted {
			return nil, fmt.Errorf("file %s was deleted by snapshot %s", name, item.Digest)
		}
		if found {
			return content, nil
		}
	}
	return nil, fmt.Errorf("file %s not found in snapshot %s", name, digest)
}

func ReadSnapshotFileRange(workspaceRoot, ref, name string, offset, length int64, opts ReadFileOptions) ([]byte, error) {
	if offset < 0 {
		return nil, fmt.Errorf("range offset must be non-negative")
	}
	if length < 0 {
		return nil, fmt.Errorf("range length must be non-negative")
	}
	s, err := findStore(workspaceRoot)
	if err != nil {
		return nil, err
	}
	digest, err := s.resolveRef(ref)
	if err != nil {
		return nil, err
	}
	chain, err := s.snapshotChainWithDigests(digest)
	if err != nil {
		return nil, err
	}
	name, err = canonicalReadPath(name)
	if err != nil {
		return nil, err
	}
	for i := len(chain) - 1; i >= 0; i-- {
		item := chain[i]
		if len(item.Manifest.Layers) != 1 {
			return nil, fmt.Errorf("local prototype expects exactly one layer, got %d", len(item.Manifest.Layers))
		}
		layerDesc := item.Manifest.Layers[0]
		if layerDesc.MediaType == MediaTypeLayerEnc {
			if opts.Decrypt == "" {
				return nil, fmt.Errorf("lazy encrypted file range reads require decrypt material")
			}
			content, found, deleted, err := readEncryptedLazyFileRange(s, item.Digest, name, opts.Decrypt, offset, length)
			if err != nil {
				return nil, err
			}
			if deleted {
				return nil, fmt.Errorf("file %s was deleted by snapshot %s", name, item.Digest)
			}
			if found {
				return content, nil
			}
		}
		content, err := ReadSnapshotFile(workspaceRoot, item.Digest, name, opts)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				continue
			}
			return nil, err
		}
		return sliceRange(content, offset, length), nil
	}
	return nil, fmt.Errorf("file %s not found in snapshot %s", name, digest)
}

func sliceRange(data []byte, offset, length int64) []byte {
	if offset >= int64(len(data)) || length == 0 {
		return []byte{}
	}
	end := offset + length
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return data[offset:end]
}

func canonicalReadPath(name string) (string, error) {
	name = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(name)), "/")
	if name == "" {
		return "", fmt.Errorf("file path is required")
	}
	return canonicalLayerPath(name)
}

func readFileFromLayer(data []byte, name string) ([]byte, bool, bool, error) {
	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false, false, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, false, false, nil
		}
		if err != nil {
			return nil, false, false, err
		}
		clean, err := canonicalLayerPath(hdr.Name)
		if err != nil {
			return nil, false, false, err
		}
		if strings.HasPrefix(filepath.Base(clean), ".wh.") {
			if err := validateWhiteoutHeader(hdr); err != nil {
				return nil, false, false, err
			}
			if whiteoutTarget(clean) == name {
				return nil, false, true, nil
			}
			continue
		}
		if clean != name {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			content, err := io.ReadAll(tr)
			return content, true, false, err
		case tar.TypeSymlink:
			return []byte(hdr.Linkname), true, false, nil
		default:
			return nil, false, false, fmt.Errorf("%s is not a file", name)
		}
	}
}

func whiteoutTarget(whiteout string) string {
	dir, base := filepath.Split(filepath.ToSlash(whiteout))
	if !strings.HasPrefix(base, ".wh.") {
		return ""
	}
	target := strings.TrimPrefix(base, ".wh.")
	if target == "" || target == "." || target == ".." || strings.ContainsAny(target, `/\`) {
		return ""
	}
	return filepath.ToSlash(filepath.Join(dir, target))
}
