package signing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
)

const (
	// CurrentFormatVersion is the detached signature document version.
	CurrentFormatVersion = 1
	// Algorithm identifies the signature primitive used by this format.
	Algorithm = "Ed25519"
	domain    = "fluxa-package-signature-v1\x00"
)

// Document is the deterministic detached signature format.
type Document struct {
	FormatVersion int    `json:"format_version"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	PackageSHA256 string `json:"package_sha256"`
	Signature     string `json:"signature"`
}

// Result describes a written or verified signature.
type Result struct {
	Path          string
	SHA256        string
	KeyID         string
	PackageSHA256 string
}

// Sign verifies a package, loads a raw Fluxa Ed25519 private key, and writes a detached signature.
func Sign(packagePath, privateKeyPath, outputPath string) (Result, error) {
	if packagePath == "" || privateKeyPath == "" || outputPath == "" {
		return Result{}, signingError(ErrorInvalidInput, "validate sign request", "", errors.New("package, private key, and output paths are required"))
	}
	packageInfo, err := flxpkg.Verify(packagePath)
	if err != nil {
		return Result{}, signingError(ErrorIntegrity, "verify package", packagePath, err)
	}
	privateKey, err := loadPrivateKey(privateKeyPath)
	if err != nil {
		return Result{}, err
	}
	defer clear(privateKey)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyID := hashBytes(publicKey)
	digest, err := hex.DecodeString(packageInfo.SHA256)
	if err != nil {
		return Result{}, signingError(ErrorIntegrity, "decode package hash", packagePath, err)
	}
	signature := ed25519.Sign(privateKey, signingMessage(digest))
	document := Document{
		FormatVersion: CurrentFormatVersion,
		Algorithm:     Algorithm,
		KeyID:         keyID,
		PackageSHA256: packageInfo.SHA256,
		Signature:     base64.StdEncoding.EncodeToString(signature),
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Result{}, signingError(ErrorInvalidInput, "encode signature", outputPath, err)
	}
	data = append(data, '\n')
	if err := writeExclusive(outputPath, data); err != nil {
		return Result{}, err
	}
	return Result{
		Path: outputPath, SHA256: hashBytes(data), KeyID: keyID,
		PackageSHA256: packageInfo.SHA256,
	}, nil
}

// Verify verifies package integrity, the detached document, key identity, and signature.
func Verify(packagePath, signaturePath, publicKeyPath string) (Result, error) {
	if packagePath == "" || signaturePath == "" || publicKeyPath == "" {
		return Result{}, signingError(ErrorInvalidInput, "validate verify request", "", errors.New("package, signature, and public key paths are required"))
	}
	packageInfo, err := flxpkg.Verify(packagePath)
	if err != nil {
		return Result{}, signingError(ErrorIntegrity, "verify package", packagePath, err)
	}
	publicKey, err := loadPublicKey(publicKeyPath)
	if err != nil {
		return Result{}, err
	}
	data, document, err := readDocument(signaturePath)
	if err != nil {
		return Result{}, err
	}
	keyID := hashBytes(publicKey)
	if document.KeyID != keyID {
		return Result{}, signingError(ErrorSignature, "verify key identity", signaturePath, errors.New("signature key ID does not match public key"))
	}
	if document.PackageSHA256 != packageInfo.SHA256 {
		return Result{}, signingError(ErrorIntegrity, "verify signed package hash", packagePath, errors.New("package hash does not match signature"))
	}
	digest, err := hex.DecodeString(packageInfo.SHA256)
	if err != nil {
		return Result{}, signingError(ErrorIntegrity, "decode package hash", packagePath, err)
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(document.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Result{}, signingError(ErrorSignature, "decode signature", signaturePath, errors.New("invalid Ed25519 signature encoding"))
	}
	if !ed25519.Verify(publicKey, signingMessage(digest), signature) {
		return Result{}, signingError(ErrorSignature, "verify signature", signaturePath, errors.New("invalid Ed25519 signature"))
	}
	return Result{
		Path: signaturePath, SHA256: hashBytes(data), KeyID: keyID,
		PackageSHA256: packageInfo.SHA256,
	}, nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, mode, err := readKey(path)
	if err != nil {
		return nil, err
	}
	if len(data) != ed25519.PrivateKeySize {
		return nil, signingError(ErrorKey, "load private key", path, fmt.Errorf("expected %d raw bytes", ed25519.PrivateKeySize))
	}
	if goruntime.GOOS != "windows" && mode.Perm()&0o077 != 0 {
		return nil, signingError(ErrorKey, "validate private key permissions", path, errors.New("group or other permissions are not allowed"))
	}
	derived := ed25519.NewKeyFromSeed(data[:ed25519.SeedSize])
	if !bytes.Equal(derived, data) {
		clear(derived)
		return nil, signingError(ErrorKey, "validate private key", path, errors.New("public key suffix does not match private seed"))
	}
	clear(derived)
	key := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(key, data)
	return key, nil
}

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	data, _, err := readKey(path)
	if err != nil {
		return nil, err
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, signingError(ErrorKey, "load public key", path, fmt.Errorf("expected %d raw bytes", ed25519.PublicKeySize))
	}
	key := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(key, data)
	return key, nil
}

func readKey(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, signingError(ErrorKey, "inspect key file", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, signingError(ErrorKey, "validate key file", path, errors.New("must be a non-symlink regular file"))
	}
	data, err := os.ReadFile(path) // #nosec G304 -- caller-selected, validated key path.
	if err != nil {
		return nil, 0, signingError(ErrorKey, "read key file", path, err)
	}
	return data, info.Mode(), nil
}

func readDocument(path string) ([]byte, Document, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, Document{}, signingError(ErrorInvalidInput, "validate signature file", path, errors.New("must be a non-symlink regular file"))
	}
	data, err := os.ReadFile(path) // #nosec G304 -- validated signature path.
	if err != nil {
		return nil, Document{}, signingError(ErrorIO, "read signature", path, err)
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, Document{}, signingError(ErrorSignature, "decode signature document", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, Document{}, signingError(ErrorSignature, "decode signature document", path, errors.New("must contain exactly one JSON document"))
	}
	if document.FormatVersion != CurrentFormatVersion || document.Algorithm != Algorithm ||
		len(document.KeyID) != sha256.Size*2 || len(document.PackageSHA256) != sha256.Size*2 ||
		document.Signature == "" {
		return nil, Document{}, signingError(ErrorSignature, "validate signature document", path, errors.New("invalid or unsupported signature metadata"))
	}
	return data, document, nil
}

func writeExclusive(path string, data []byte) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return signingError(ErrorInvalidInput, "validate signature output directory", parent, errors.New("must be an existing non-symlink directory"))
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- validated output parent.
	if err != nil {
		return signingError(ErrorIO, "create signature", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return signingError(ErrorIO, "write signature", path, err)
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		_ = os.Remove(path)
		return signingError(ErrorIO, "finish signature", path, err)
	}
	return nil
}

func signingMessage(digest []byte) []byte {
	message := make([]byte, 0, len(domain)+len(digest))
	message = append(message, domain...)
	message = append(message, digest...)
	return message
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
