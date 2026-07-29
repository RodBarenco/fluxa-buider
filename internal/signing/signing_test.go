package signing_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/signing"
)

func TestSignAndVerifyWithRawFluxaKeys(t *testing.T) {
	fixture := newSigningFixture(t)
	signed, err := signing.Sign(fixture.packagePath, fixture.privatePath, fixture.signaturePath)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := signing.Verify(fixture.packagePath, fixture.signaturePath, fixture.publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if verified.KeyID != signed.KeyID || verified.PackageSHA256 != signed.PackageSHA256 ||
		verified.SHA256 != signed.SHA256 {
		t.Fatalf("signed=%#v verified=%#v", signed, verified)
	}
	signatureData, err := os.ReadFile(fixture.signaturePath) // #nosec G304 -- test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(signatureData, fixture.privateKey) ||
		bytes.Contains(signatureData, []byte(hex.EncodeToString(fixture.privateKey))) ||
		bytes.Contains(signatureData, []byte(base64.StdEncoding.EncodeToString(fixture.privateKey))) {
		t.Fatal("signature document contains private key material")
	}
}

func TestVerifyRejectsInvalidSignatureWrongKeyAndChangedPackage(t *testing.T) {
	t.Run("invalid signature", func(t *testing.T) {
		fixture := newSigningFixture(t)
		if _, err := signing.Sign(fixture.packagePath, fixture.privatePath, fixture.signaturePath); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(fixture.signaturePath) // #nosec G304 -- test fixture.
		if err != nil {
			t.Fatal(err)
		}
		var document signing.Document
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		signature, err := base64.StdEncoding.DecodeString(document.Signature)
		if err != nil {
			t.Fatal(err)
		}
		signature[0] ^= 1
		document.Signature = base64.StdEncoding.EncodeToString(signature)
		writeJSON(t, fixture.signaturePath, document)
		if _, err := signing.Verify(fixture.packagePath, fixture.signaturePath, fixture.publicPath); err == nil {
			t.Fatal("Verify() accepted modified signature")
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		fixture := newSigningFixture(t)
		if _, err := signing.Sign(fixture.packagePath, fixture.privatePath, fixture.signaturePath); err != nil {
			t.Fatal(err)
		}
		wrongPublic, _, _ := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{9}, 32)))
		wrongPath := filepath.Join(t.TempDir(), "wrong.pub")
		if err := os.WriteFile(wrongPath, wrongPublic, 0o400); err != nil {
			t.Fatal(err)
		}
		if _, err := signing.Verify(fixture.packagePath, fixture.signaturePath, wrongPath); err == nil {
			t.Fatal("Verify() accepted wrong public key")
		}
	})

	t.Run("changed package byte", func(t *testing.T) {
		fixture := newSigningFixture(t)
		if _, err := signing.Sign(fixture.packagePath, fixture.privatePath, fixture.signaturePath); err != nil {
			t.Fatal(err)
		}
		mutateByte(t, fixture.packagePath, -1)
		if _, err := signing.Verify(fixture.packagePath, fixture.signaturePath, fixture.publicPath); err == nil {
			t.Fatal("Verify() accepted changed package")
		}
	})

	t.Run("changed manifest byte", func(t *testing.T) {
		fixture := newSigningFixture(t)
		if _, err := signing.Sign(fixture.packagePath, fixture.privatePath, fixture.signaturePath); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(fixture.packagePath) // #nosec G304 -- test fixture.
		if err != nil {
			t.Fatal(err)
		}
		index := bytes.Index(data, []byte("Game"))
		if index < 0 {
			t.Fatal("manifest marker not found")
		}
		mutateByte(t, fixture.packagePath, index)
		if _, err := signing.Verify(fixture.packagePath, fixture.signaturePath, fixture.publicPath); err == nil {
			t.Fatal("Verify() accepted changed manifest")
		}
	})
}

func TestKeysAreRequiredValidatedAndNeverIncludedInErrors(t *testing.T) {
	fixture := newSigningFixture(t)
	secretHex := hex.EncodeToString(fixture.privateKey)

	if _, err := signing.Sign(fixture.packagePath, "", fixture.signaturePath); err == nil {
		t.Fatal("Sign() accepted missing key path")
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid.key")
	if err := os.WriteFile(invalidPath, []byte("secret-material-that-must-not-leak"), 0o400); err != nil {
		t.Fatal(err)
	}
	_, err := signing.Sign(fixture.packagePath, invalidPath, fixture.signaturePath)
	if err == nil {
		t.Fatal("Sign() accepted invalid key")
	}
	if strings.Contains(err.Error(), "secret-material") || strings.Contains(err.Error(), secretHex) {
		t.Fatalf("error leaked key material: %v", err)
	}

	if _, err := signing.Verify(fixture.packagePath, filepath.Join(t.TempDir(), "missing.sig"), fixture.publicPath); err == nil {
		t.Fatal("Verify() accepted missing signature")
	}
}

type signingFixture struct {
	packagePath   string
	privatePath   string
	publicPath    string
	signaturePath string
	privateKey    ed25519.PrivateKey
}

func newSigningFixture(t *testing.T) signingFixture {
	t.Helper()
	root := t.TempDir()
	packagePath := writeTestPackage(t, root)
	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(root, "signing.key")
	publicPath := filepath.Join(root, "signing.pub")
	if err := os.WriteFile(privatePath, privateKey, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, publicKey, 0o400); err != nil {
		t.Fatal(err)
	}
	return signingFixture{
		packagePath: packagePath, privatePath: privatePath, publicPath: publicPath,
		signaturePath: packagePath + ".sig", privateKey: privateKey,
	}
}

func writeTestPackage(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "main.flx")
	data := []byte(`print("signed")`)
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	file := manifest.File{
		Path: "program/source/main.flx", LogicalPath: "main.flx", Kind: "program",
		Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
	}
	hash := strings.Repeat("a", 64)
	value := manifest.Manifest{
		FormatVersion: manifest.CurrentFormatVersion,
		Project: manifest.Project{
			Name: "Game", ID: "com.example.game", Version: "1.0.0", Entry: "main.flx", Type: "desktop",
		},
		Toolchain: manifest.Toolchain{
			Protocol: "runtime-info-v1", FluxaSHA256: hash, LibrariesSHA256: hash,
		},
		Target: manifest.Target{OS: "linux", Arch: "amd64", Terminal: true},
		Build: manifest.Build{
			Preflight: "not_run", ProgramFormat: "fluxa-source", Debug: true, SourceExposed: true,
		},
		Files: []manifest.File{file},
	}
	output := filepath.Join(root, "game.flxpkg")
	if _, err := flxpkg.Write(context.Background(), flxpkg.Request{
		OutputPath: output, Manifest: value, Sources: map[string]string{file.Path: source},
	}); err != nil {
		t.Fatal(err)
	}
	return output
}

func mutateByte(t *testing.T, filePath string, index int) {
	t.Helper()
	data, err := os.ReadFile(filePath) // #nosec G304 -- test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if index < 0 {
		index = len(data) - 1
	}
	data[index] ^= 1
	if err := os.WriteFile(filePath, data, 0o600); err != nil { // #nosec G703 -- test-controlled fixture path.
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, filePath string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
