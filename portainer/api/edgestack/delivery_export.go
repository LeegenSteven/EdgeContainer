package edgestack

import (
	"archive/tar"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type deliveryDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}
type deliveryOCIEntry struct {
	size int64
	body []byte
}

func deliveryStreamOCI(input io.Reader, platform string) (string, error) {
	archive := tar.NewReader(input)
	entries := map[string]deliveryOCIEntry{}
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			return "", fail(409, "invalid_oci_archive")
		}
		if _, ok := entries[name]; ok {
			return "", fail(409, "duplicate_oci_entry")
		}
		hash := sha256.New()
		entry := deliveryOCIEntry{size: header.Size}
		reader := io.TeeReader(archive, hash)
		if header.Size <= 4<<20 {
			entry.body, err = io.ReadAll(reader)
		} else {
			_, err = io.Copy(io.Discard, reader)
		}
		if err != nil {
			return "", err
		}
		actual := "sha256:" + hex.EncodeToString(hash.Sum(nil))
		if strings.HasPrefix(name, "blobs/sha256/") && actual != "sha256:"+strings.TrimPrefix(name, "blobs/sha256/") {
			return "", fail(409, "oci_blob_digest_mismatch")
		}
		entries[name] = entry
	}
	var index struct {
		SchemaVersion int                  `json:"schemaVersion"`
		Manifests     []deliveryDescriptor `json:"manifests"`
	}
	if err := json.Unmarshal(entries["index.json"].body, &index); err != nil || index.SchemaVersion != 2 || len(index.Manifests) != 1 {
		return "", fail(409, "invalid_oci_index")
	}
	resolve := func(descriptor deliveryDescriptor) (deliveryOCIEntry, error) {
		if !digest.MatchString(descriptor.Digest) {
			return deliveryOCIEntry{}, fail(409, "invalid_oci_descriptor")
		}
		entry, ok := entries["blobs/sha256/"+strings.TrimPrefix(descriptor.Digest, "sha256:")]
		if !ok || entry.size != descriptor.Size {
			return entry, fail(409, "oci_descriptor_mismatch")
		}
		return entry, nil
	}
	manifestBytes, err := resolve(index.Manifests[0])
	if err != nil {
		return "", err
	}
	var manifest struct {
		SchemaVersion int                  `json:"schemaVersion"`
		Config        deliveryDescriptor   `json:"config"`
		Layers        []deliveryDescriptor `json:"layers"`
	}
	if err = json.Unmarshal(manifestBytes.body, &manifest); err != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) == 0 {
		return "", fail(409, "invalid_oci_manifest")
	}
	configBytes, err := resolve(manifest.Config)
	if err != nil {
		return "", err
	}
	var config struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	}
	if err = json.Unmarshal(configBytes.body, &config); err != nil || config.OS+"/"+config.Architecture != platform {
		return "", fail(409, "oci_platform_mismatch")
	}
	for _, layer := range manifest.Layers {
		if _, err = resolve(layer); err != nil {
			return "", err
		}
	}
	return index.Manifests[0].Digest, nil
}
func (s *Service) openDeliveryBundle(artifact DeliveryArtifact) (*os.File, error) {
	root := s.opts.DeliveryArtifactRoot
	if root == "" {
		root = os.Getenv("EDGESTACK_DELIVERY_ARTIFACT_ROOT")
	}
	if root == "" {
		return nil, fail(503, "delivery_artifact_root_unconfigured")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(artifact.OfflineBundle.URI)
	if err != nil || parsed.Scheme != "file" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fail(409, "artifact_location_invalid")
	}
	path := filepath.Clean(parsed.Path)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fail(409, "artifact_location_forbidden")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil {
			return nil, fail(404, "artifact_file_unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fail(409, "artifact_symlink_forbidden")
		}
		if current == root {
			break
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fail(404, "artifact_file_unavailable")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fail(409, "artifact_file_invalid")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != artifact.OfflineBundle.SHA256 {
		_ = file.Close()
		return nil, fail(409, "artifact_bundle_hash_mismatch")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
func (s *Service) verifyDeliveryBundle(file *os.File, artifact DeliveryArtifact) error {
	keyPath := s.opts.DeliveryTrustedPublicKeyFile
	if keyPath == "" {
		keyPath = os.Getenv("EDGESTACK_DELIVERY_TRUSTED_PUBLIC_KEY_FILE")
	}
	if keyPath == "" {
		return fail(503, "delivery_trusted_key_unconfigured")
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return fail(503, "delivery_trusted_key_unavailable")
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return fail(503, "delivery_trusted_key_invalid")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fail(503, "delivery_trusted_key_invalid")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return fail(503, "delivery_trusted_key_invalid")
	}
	archive := tar.NewReader(file)
	seen := map[string]bool{}
	var manifest, signature []byte
	var imageHash, imageDigest string
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if seen[header.Name] || header.Typeflag != tar.TypeReg {
			return fail(409, "invalid_delivery_bundle")
		}
		seen[header.Name] = true
		switch header.Name {
		case "manifest.json":
			if header.Size > 1<<20 {
				return fail(409, "bundle_manifest_too_large")
			}
			manifest, err = io.ReadAll(archive)
		case "manifest.ed25519":
			if header.Size != ed25519.SignatureSize {
				return fail(409, "bundle_signature_invalid")
			}
			signature, err = io.ReadAll(archive)
		case "image.oci.tar":
			if header.Size > 64<<30 {
				return fail(409, "bundle_image_too_large")
			}
			hash := sha256.New()
			reader := io.TeeReader(archive, hash)
			imageDigest, err = deliveryStreamOCI(reader, artifact.Platform)
			if err == nil {
				_, err = io.Copy(io.Discard, reader)
			}
			imageHash = "sha256:" + hex.EncodeToString(hash.Sum(nil))
		default:
			return fail(409, "invalid_delivery_bundle")
		}
		if err != nil {
			return err
		}
	}
	if len(seen) != 3 || !ed25519.Verify(key, manifest, signature) {
		return fail(409, "bundle_signature_invalid")
	}
	var record struct {
		SchemaVersion        int            `json:"schemaVersion"`
		JobID                string         `json:"jobId"`
		Image                string         `json:"image"`
		ImageDigest          string         `json:"imageDigest"`
		ImageArchiveSHA256   string         `json:"imageArchiveSha256"`
		ModelHash            string         `json:"modelHash"`
		ModelID              string         `json:"modelId"`
		RecipeDigest         string         `json:"recipeDigest"`
		RuntimeProfileDigest string         `json:"runtimeProfileDigest"`
		ReleaseDigest        string         `json:"releaseDigest"`
		Report               DeliveryReport `json:"report"`
	}
	if err = json.Unmarshal(manifest, &record); err != nil {
		return fail(409, "bundle_manifest_invalid")
	}
	if record.SchemaVersion != 1 || record.JobID != artifact.JobID || record.Image != artifact.Image || !strings.HasSuffix(artifact.Image, "@"+imageDigest) || record.ImageDigest != imageDigest || record.ImageArchiveSHA256 != imageHash || record.ModelHash != artifact.ModelHash || record.ModelID != artifact.ModelID || record.RecipeDigest != artifact.RecipeDigest || record.RuntimeProfileDigest != artifact.RuntimeProfileDigest || record.ReleaseDigest != artifact.ReleaseDigest || record.Report.Digest != artifact.ReportDigest || deliveryDigest(record.Report) != artifact.ReportDigest {
		return fail(409, "bundle_manifest_mismatch")
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}
func (s *Service) deliveryDownload(w http.ResponseWriter, r *http.Request) error {
	response := &deliveryResponse{header: http.Header{}}
	if err := s.deliveryRead(response, r, "artifacts", true); err != nil {
		return err
	}
	var artifact DeliveryArtifact
	if err := json.Unmarshal(response.Bytes(), &artifact); err != nil {
		return err
	}
	file, err := s.openDeliveryBundle(artifact)
	if err != nil {
		return err
	}
	defer file.Close()
	if err = s.verifyDeliveryBundle(file, artifact); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	checksum, _ := hex.DecodeString(strings.TrimPrefix(artifact.OfflineBundle.SHA256, "sha256:"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.oci-bundle.tar\"", artifact.ID))
	w.Header().Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(checksum)+":")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, artifact.ID+".tar", info.ModTime(), file)
	return nil
}
