package coding

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"golang.org/x/crypto/ssh"
)

const commitPayload = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nauthor Ada <ada@example.com> 1700000000 +0000\ncommitter Ada <ada@example.com> 1700000000 +0000\n\nAdd feature\n\nCo-authored-by: Flow <bot@example.com>\n"

func sshKeyPEM(t *testing.T, key any, passphrase string) []byte {
	t.Helper()
	var block *pem.Block
	var err error
	if passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "test", []byte(passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(key, "test")
	}
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block)
}

func sshKeys(t *testing.T) map[string][]byte {
	t.Helper()
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"ed25519": sshKeyPEM(t, edKey, ""), "ecdsa": sshKeyPEM(t, ecKey, ""), "rsa": sshKeyPEM(t, rsaKey, "")}
}

// verifySSHSIG is an independent PROTOCOL.sshsig verifier built on
// golang.org/x/crypto/ssh: it parses the armored blob and checks the
// signature with the embedded public key.
func verifySSHSIG(t *testing.T, armored string, message []byte, namespace string, want ssh.PublicKey) {
	t.Helper()
	body, ok := strings.CutPrefix(armored, "-----BEGIN SSH SIGNATURE-----\n")
	if !ok || !strings.HasSuffix(body, "\n-----END SSH SIGNATURE-----\n") {
		t.Fatalf("bad armor: %q", armored)
	}
	body = strings.TrimSuffix(body, "\n-----END SSH SIGNATURE-----\n")
	for _, line := range strings.Split(body, "\n") {
		if len(line) > 70 {
			t.Fatalf("armor line longer than 70 columns: %d", len(line))
		}
	}
	blob, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := bytes.CutPrefix(blob, []byte("SSHSIG"))
	if !ok {
		t.Fatal("missing SSHSIG preamble")
	}
	var sig struct {
		Version       uint32
		PublicKey     string
		Namespace     string
		Reserved      string
		HashAlgorithm string
		Signature     string
	}
	if err := ssh.Unmarshal(rest, &sig); err != nil {
		t.Fatal(err)
	}
	if sig.Version != 1 || sig.Namespace != namespace || sig.Reserved != "" || sig.HashAlgorithm != "sha512" {
		t.Fatalf("sshsig header: %+v", sig)
	}
	publicKey, err := ssh.ParsePublicKey([]byte(sig.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(publicKey.Marshal(), want.Marshal()) {
		t.Fatal("signature embeds another public key")
	}
	var signature ssh.Signature
	if err := ssh.Unmarshal([]byte(sig.Signature), &signature); err != nil {
		t.Fatal(err)
	}
	if publicKey.Type() == ssh.KeyAlgoRSA && signature.Format != ssh.KeyAlgoRSASHA512 {
		t.Fatalf("RSA signature uses %s, want rsa-sha2-512", signature.Format)
	}
	digest := sha512.Sum512(message)
	signed := append([]byte("SSHSIG"), ssh.Marshal(struct {
		Namespace, Reserved, HashAlgorithm, Hash string
	}{namespace, "", "sha512", string(digest[:])})...)
	if err := publicKey.Verify(signed, &signature); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	if err := publicKey.Verify(append(signed, 'x'), &signature); err == nil {
		t.Fatal("signature verified over different data")
	}
}

func TestSignSSHProducesVerifiableSSHSIG(t *testing.T) {
	for name, keyPEM := range sshKeys(t) {
		t.Run(name, func(t *testing.T) {
			key, err := ParseSigningKey(keyPEM)
			if err != nil {
				t.Fatal(err)
			}
			if key.Format != SigningFormatSSH || !strings.HasPrefix(key.Fingerprint, "SHA256:") {
				t.Fatalf("key: %+v", key)
			}
			publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key.PublicKey))
			if err != nil {
				t.Fatalf("public key line %q: %v", key.PublicKey, err)
			}
			armored, err := Sign(SigningFormatSSH, keyPEM, []byte(commitPayload))
			if err != nil {
				t.Fatal(err)
			}
			verifySSHSIG(t, armored, []byte(commitPayload), SSHSignatureNamespace, publicKey)
			verifyWithSSHKeygen(t, key.PublicKey, armored, commitPayload)
		})
	}
}

// verifyWithSSHKeygen checks interoperability with OpenSSH, which is what git
// itself runs to verify ssh-signed commits (`git log --show-signature`).
func verifyWithSSHKeygen(t *testing.T, publicKey, signature, payload string) {
	t.Helper()
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Log("ssh-keygen not installed; skipping the OpenSSH interop check")
		return
	}
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed_signers")
	sigFile := filepath.Join(dir, "payload.sig")
	if err := os.WriteFile(allowed, []byte("ada@example.com "+publicKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigFile, []byte(signature), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(data string) (string, error) {
		cmd := exec.Command(keygen, "-Y", "verify", "-f", allowed, "-I", "ada@example.com", "-n", "git", "-s", sigFile)
		cmd.Stdin = strings.NewReader(data)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(payload); err != nil {
		t.Fatalf("ssh-keygen -Y verify failed: %v\n%s", err, out)
	}
	if _, err := run(payload + "tampered"); err == nil {
		t.Fatal("ssh-keygen verified a tampered payload")
	}
}

func openPGPKey(t *testing.T, algorithm packet.PublicKeyAlgorithm, passphrase string) (*openpgp.Entity, []byte) {
	t.Helper()
	config := &packet.Config{Algorithm: algorithm, RSABits: 2048}
	entity, err := openpgp.NewEntity("Ada", "", "ada@example.com", config)
	if err != nil {
		t.Fatal(err)
	}
	var public bytes.Buffer
	if err := entity.Serialize(&public); err != nil {
		t.Fatal(err)
	}
	publicOnly, err := openpgp.ReadKeyRing(&public)
	if err != nil {
		t.Fatal(err)
	}
	if passphrase != "" {
		if err := entity.EncryptPrivateKeys([]byte(passphrase), nil); err != nil {
			t.Fatal(err)
		}
	}
	var armored bytes.Buffer
	writer, err := armor.Encode(&armored, openpgp.PrivateKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if passphrase != "" {
		err = entity.SerializePrivateWithoutSigning(writer, nil)
	} else {
		err = entity.SerializePrivate(writer, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return publicOnly[0], armored.Bytes()
}

func TestSignOpenPGPProducesVerifiableDetachedSignature(t *testing.T) {
	for name, algorithm := range map[string]packet.PublicKeyAlgorithm{"eddsa": packet.PubKeyAlgoEdDSA, "rsa": packet.PubKeyAlgoRSA} {
		t.Run(name, func(t *testing.T) {
			public, private := openPGPKey(t, algorithm, "")
			key, err := ParseSigningKey(private)
			if err != nil {
				t.Fatal(err)
			}
			if key.Format != SigningFormatOpenPGP || len(key.Fingerprint) != 40 || len(key.PublicKey) != 16 {
				t.Fatalf("key: %+v", key)
			}
			signature, err := Sign("", private, []byte(commitPayload))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(signature, "-----BEGIN PGP SIGNATURE-----") {
				t.Fatalf("not an armored signature: %q", signature)
			}
			signer, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{public}, strings.NewReader(commitPayload), strings.NewReader(signature), nil)
			if err != nil {
				t.Fatalf("signature does not verify: %v", err)
			}
			if signer.PrimaryKey.KeyId != public.PrimaryKey.KeyId {
				t.Fatal("wrong signer")
			}
			if _, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{public}, strings.NewReader(commitPayload+"x"), strings.NewReader(signature), nil); err == nil {
				t.Fatal("signature verified over a tampered payload")
			}
			verifyWithGPG(t, public, signature, commitPayload)
		})
	}
}

// verifyWithGPG checks interoperability with GnuPG, which git runs for
// openpgp signatures, when gpg is installed.
func verifyWithGPG(t *testing.T, public *openpgp.Entity, signature, payload string) {
	t.Helper()
	gpg, err := exec.LookPath("gpg")
	if err != nil {
		t.Log("gpg not installed; skipping the GnuPG interop check")
		return
	}
	home, err := os.MkdirTemp("", "gpg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	_ = os.Chmod(home, 0o700)
	var armored bytes.Buffer
	writer, _ := armor.Encode(&armored, openpgp.PublicKeyType, nil)
	_ = public.Serialize(writer)
	_ = writer.Close()
	keyFile, sigFile, dataFile := filepath.Join(home, "key.asc"), filepath.Join(home, "data.sig"), filepath.Join(home, "data")
	_ = os.WriteFile(keyFile, armored.Bytes(), 0o600)
	_ = os.WriteFile(sigFile, []byte(signature), 0o600)
	_ = os.WriteFile(dataFile, []byte(payload), 0o600)
	if out, err := exec.Command(gpg, "--homedir", home, "--batch", "--quiet", "--import", keyFile).CombinedOutput(); err != nil {
		t.Logf("gpg cannot import keys in this environment; skipping the interop check: %v %s", err, out)
		return
	}
	defer exec.Command("gpgconf", "--homedir", home, "--kill", "all").Run()
	if out, err := exec.Command(gpg, "--homedir", home, "--batch", "--verify", sigFile, dataFile).CombinedOutput(); err != nil {
		t.Fatalf("gpg --verify failed: %v\n%s", err, out)
	}
	_ = os.WriteFile(dataFile, []byte(payload+"x"), 0o600)
	if err := exec.Command(gpg, "--homedir", home, "--batch", "--verify", sigFile, dataFile).Run(); err == nil {
		t.Fatal("gpg verified a tampered payload")
	}
}

func TestParseSigningKeyRejectsUnusableKeys(t *testing.T) {
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	_, encryptedPGP := openPGPKey(t, packet.PubKeyAlgoEdDSA, "secret")
	public, _ := openPGPKey(t, packet.PubKeyAlgoEdDSA, "")
	var publicArmored bytes.Buffer
	writer, _ := armor.Encode(&publicArmored, openpgp.PublicKeyType, nil)
	_ = public.Serialize(writer)
	_ = writer.Close()
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"empty", nil, ErrSigningKeyInvalid},
		{"garbage", []byte("not a key"), ErrSigningKeyInvalid},
		{"encrypted ssh", sshKeyPEM(t, edKey, "secret"), ErrSigningKeyEncrypted},
		{"encrypted openpgp", encryptedPGP, ErrSigningKeyEncrypted},
		{"pgp armor without a key", []byte("-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nAAAA\n-----END PGP PRIVATE KEY BLOCK-----\n"), ErrSigningKeyInvalid},
		{"public openpgp key", bytes.Replace(publicArmored.Bytes(), []byte("PUBLIC KEY"), []byte("PRIVATE KEY"), 2), ErrSigningKeyInvalid},
	}
	for _, tc := range cases {
		if _, err := ParseSigningKey(tc.raw); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := Sign(SigningFormatOpenPGP, sshKeyPEM(t, edKey, ""), []byte("x")); !errors.Is(err, ErrSigningFormatMismatch) {
		t.Fatalf("format mismatch: %v", err)
	}
	if _, err := Sign("", sshKeyPEM(t, edKey, ""), make([]byte, MaxSignPayloadBytes+1)); err == nil {
		t.Fatal("oversized payload signed")
	}
}

type fakeSigningKeys struct {
	format string
	key    []byte
	err    error
}

func (f fakeSigningKeys) PutSigningKeyMaterial(context.Context, string, string, []byte) error {
	return nil
}

func (f fakeSigningKeys) SigningKeyMaterial(context.Context, string) (string, []byte, error) {
	return f.format, f.key, f.err
}

func (f fakeSigningKeys) DeleteSigningKeyMaterial(context.Context, string) error { return nil }

// storeStyleError mimics store.SigningKeyError (recognised by behaviour).
type storeStyleError struct{ notFound bool }

func (e storeStyleError) Error() string              { return "store error" }
func (e storeStyleError) NotFound() bool             { return e.notFound }
func (e storeStyleError) SigningKeyUnreadable() bool { return !e.notFound }

func TestResolveCommitSigning(t *testing.T) {
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	valid := sshKeyPEM(t, edKey, "")
	transient := errors.New("database is down")
	cases := []struct {
		name     string
		keys     SigningKeyStore
		required bool
		legacy   bool
		format   string
		code     domain.CodingErrorCode
		message  string
		err      error
	}{
		{name: "optional and missing: unsigned", keys: fakeSigningKeys{err: ErrNotFound}},
		{name: "optional with key: signed", keys: fakeSigningKeys{format: "ssh", key: valid}, format: SigningFormatSSH},
		{name: "required with key", keys: fakeSigningKeys{format: "ssh", key: valid}, required: true, format: SigningFormatSSH},
		{name: "required and missing", keys: fakeSigningKeys{err: storeStyleError{notFound: true}}, required: true, code: domain.CodingErrMissingCommitSigningKey, message: MissingSigningKeyMessage},
		{name: "required, legacy upload without material", keys: fakeSigningKeys{err: ErrNotFound}, required: true, legacy: true, code: domain.CodingErrMissingCommitSigningKey, message: LegacySigningKeyMessage},
		{name: "required, undecryptable", keys: fakeSigningKeys{err: storeStyleError{}}, required: true, code: domain.CodingErrMissingCommitSigningKey, message: UnreadableSigningKeyMsg},
		{name: "optional, undecryptable: unsigned", keys: fakeSigningKeys{err: storeStyleError{}}},
		{name: "required, corrupt material", keys: fakeSigningKeys{format: "ssh", key: []byte("junk")}, required: true, code: domain.CodingErrMissingCommitSigningKey, message: UnreadableSigningKeyMsg},
		{name: "required, no store", required: true, code: domain.CodingErrMissingCommitSigningKey, message: MissingSigningKeyMessage},
		{name: "store outage is transient", keys: fakeSigningKeys{err: transient}, required: true, err: transient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolution, err := ResolveCommitSigning(context.Background(), tc.keys, "user", tc.required, tc.legacy)
			var signingErr *CommitSigningError
			switch {
			case tc.code != "":
				if !errors.As(err, &signingErr) || signingErr.Code != tc.code || signingErr.Message != tc.message || signingErr.CodingErrorCode() != tc.code {
					t.Fatalf("err %v, want %s %q", err, tc.code, tc.message)
				}
			case tc.err != nil:
				if !errors.Is(err, tc.err) || errors.As(err, &signingErr) {
					t.Fatalf("err %v, want %v", err, tc.err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if resolution.Format != tc.format || (tc.format != "" && !strings.HasPrefix(resolution.PublicKey, "ssh-ed25519 ")) {
					t.Fatalf("resolution %+v", resolution)
				}
			}
		})
	}
}

func TestSignForUser(t *testing.T) {
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	valid := sshKeyPEM(t, edKey, "")
	payload := []byte(commitPayload)
	response, err := SignForUser(context.Background(), fakeSigningKeys{format: "ssh", key: valid}, "user", SignRequest{Format: "ssh", Payload: payload}, false)
	if err != nil || !strings.HasPrefix(response.Signature, "-----BEGIN SSH SIGNATURE-----") {
		t.Fatalf("sign: %v %q", err, response.Signature)
	}
	signer, _ := ssh.NewSignerFromKey(edKey)
	verifySSHSIG(t, response.Signature, payload, "git", signer.PublicKey())

	cases := []struct {
		name    string
		keys    SigningKeyStore
		request SignRequest
		legacy  bool
		code    domain.CodingErrorCode
		message string
	}{
		{"missing key", fakeSigningKeys{err: ErrNotFound}, SignRequest{Format: "ssh", Payload: payload}, false, domain.CodingErrMissingCommitSigningKey, MissingSigningKeyMessage},
		{"legacy key", fakeSigningKeys{err: ErrNotFound}, SignRequest{Format: "ssh", Payload: payload}, true, domain.CodingErrMissingCommitSigningKey, LegacySigningKeyMessage},
		{"unreadable key", fakeSigningKeys{err: storeStyleError{}}, SignRequest{Payload: payload}, false, domain.CodingErrMissingCommitSigningKey, UnreadableSigningKeyMsg},
		{"format mismatch", fakeSigningKeys{format: "ssh", key: valid}, SignRequest{Format: "openpgp", Payload: payload}, false, domain.CodingErrSigningFailed, ""},
		{"unknown format", fakeSigningKeys{format: "ssh", key: valid}, SignRequest{Format: "x509", Payload: payload}, false, domain.CodingErrSigningFailed, ""},
		{"empty payload", fakeSigningKeys{format: "ssh", key: valid}, SignRequest{Format: "ssh"}, false, domain.CodingErrSigningFailed, ""},
		{"corrupt key", fakeSigningKeys{format: "ssh", key: []byte("junk")}, SignRequest{Payload: payload}, false, domain.CodingErrMissingCommitSigningKey, UnreadableSigningKeyMsg},
	}
	for _, tc := range cases {
		_, err := SignForUser(context.Background(), tc.keys, "user", tc.request, tc.legacy)
		var signingErr *CommitSigningError
		if !errors.As(err, &signingErr) || signingErr.Code != tc.code || (tc.message != "" && signingErr.Message != tc.message) {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
	transient := errors.New("database is down")
	if _, err := SignForUser(context.Background(), fakeSigningKeys{err: transient}, "user", SignRequest{Payload: payload}, false); !errors.Is(err, transient) {
		t.Fatalf("transient: %v", err)
	}
}

func TestSignOpenPGPUsesGivenTime(t *testing.T) {
	_, private := openPGPKey(t, packet.PubKeyAlgoEdDSA, "")
	at := time.Now().Add(time.Hour).Truncate(time.Second)
	signature, err := SignOpenPGP(private, []byte("x"), at)
	if err != nil {
		t.Fatal(err)
	}
	block, err := armor.Decode(strings.NewReader(signature))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := packet.Read(block.Body)
	if err != nil {
		t.Fatal(err)
	}
	sig, ok := parsed.(*packet.Signature)
	if !ok || !sig.CreationTime.Equal(at) {
		t.Fatalf("signature packet: %#v", parsed)
	}
}
