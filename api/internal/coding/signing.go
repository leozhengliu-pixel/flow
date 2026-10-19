package coding

// Commit signing for coding sessions (track E, docs/coding-sessions-design.md
// §16 step 3, §19 control 4, §25 risk 6). The user's private key is uploaded
// once, stored encrypted (SigningKeyStore), and never enters a sandbox: the
// runner's flow-sign shim posts the payload git wants signed to
// POST /internal/coding-runtime/sign and Flow answers with a detached
// signature in the format git expects:
//
//   - ssh: an armored SSHSIG ("-----BEGIN SSH SIGNATURE-----") over the payload
//     with namespace "git", exactly what `ssh-keygen -Y sign -n git` writes
//     (gpg.format=ssh, gpg.ssh.program=flow-sign).
//   - openpgp: an armored binary detached signature, what `gpg -bsau` writes
//     (gpg.program=flow-sign).

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"golang.org/x/crypto/ssh"
)

// Signing key formats (coding_signing_keys.format, SignRequest.Format,
// BootstrapGit.SigningFormat).
const (
	SigningFormatSSH     = "ssh"
	SigningFormatOpenPGP = "openpgp"
)

// SSHSignatureNamespace is the namespace git uses for commit and tag signatures.
const SSHSignatureNamespace = "git"

// MaxSignPayloadBytes bounds one commit or tag payload (the headers plus the
// message; git never hands file contents to the signer).
const MaxSignPayloadBytes = 1 << 20

var (
	// ErrSigningKeyInvalid means the uploaded or stored key cannot sign.
	ErrSigningKeyInvalid = errors.New("coding: invalid signing key")
	// ErrSigningKeyEncrypted means the key is passphrase-protected; Flow signs
	// unattended, so it needs the unencrypted key.
	ErrSigningKeyEncrypted = errors.New("coding: encrypted private keys are not supported")
	// ErrSigningFormatMismatch means git asked for a format the user's key is not.
	ErrSigningFormatMismatch = errors.New("coding: signing format does not match the user's key")
)

// SigningKey is the public description of a parsed private key.
type SigningKey struct {
	Format string
	// Fingerprint is "SHA256:…" for SSH keys and the uppercase hex v4/v6
	// fingerprint for OpenPGP keys.
	Fingerprint string
	// PublicKey is what git's user.signingkey is set to in the sandbox: the
	// authorized_keys line for SSH, the 16-hex-digit key id for OpenPGP.
	PublicKey string
}

// ParseSigningKey validates an unencrypted private key: an OpenSSH or PEM SSH
// key, or an ASCII-armored OpenPGP secret key.
func ParseSigningKey(raw []byte) (SigningKey, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return SigningKey{}, fmt.Errorf("%w: the private key is empty", ErrSigningKeyInvalid)
	}
	if bytes.Contains(raw, []byte("BEGIN PGP PRIVATE KEY BLOCK")) {
		entity, err := parseOpenPGPKey(raw)
		if err != nil {
			return SigningKey{}, err
		}
		key, _ := entity.SigningKey(time.Now())
		return SigningKey{Format: SigningFormatOpenPGP, Fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)), PublicKey: key.PublicKey.KeyIdString()}, nil
	}
	signer, err := parseSSHKey(raw)
	if err != nil {
		return SigningKey{}, err
	}
	return SigningKey{Format: SigningFormatSSH, Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))}, nil
}

func parseSSHKey(raw []byte) (ssh.Signer, error) {
	key, err := ssh.ParseRawPrivateKey(raw)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, ErrSigningKeyEncrypted
		}
		return nil, fmt.Errorf("%w: not an SSH private key", ErrSigningKeyInvalid)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported SSH key type", ErrSigningKeyInvalid)
	}
	return signer, nil
}

func parseOpenPGPKey(raw []byte) (*openpgp.Entity, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: not an OpenPGP private key", ErrSigningKeyInvalid)
	}
	for _, entity := range entities {
		if entity.PrivateKey == nil {
			continue
		}
		key, ok := entity.SigningKey(time.Now())
		if !ok || key.PrivateKey == nil {
			return nil, fmt.Errorf("%w: the OpenPGP key has no usable signing key (expired, revoked or missing)", ErrSigningKeyInvalid)
		}
		if key.PrivateKey.Encrypted {
			return nil, ErrSigningKeyEncrypted
		}
		return entity, nil
	}
	return nil, fmt.Errorf("%w: the OpenPGP block holds no secret key", ErrSigningKeyInvalid)
}

// Sign makes the detached signature git expects for payload with the user's
// private key. format must match the key ("" accepts the key's own format).
func Sign(format string, privateKey, payload []byte) (string, error) {
	if len(payload) > MaxSignPayloadBytes {
		return "", fmt.Errorf("coding: payload exceeds %d bytes", MaxSignPayloadBytes)
	}
	key, err := ParseSigningKey(privateKey)
	if err != nil {
		return "", err
	}
	if format != "" && format != key.Format {
		return "", ErrSigningFormatMismatch
	}
	if key.Format == SigningFormatOpenPGP {
		return SignOpenPGP(privateKey, payload, time.Now())
	}
	return SignSSH(privateKey, payload, SSHSignatureNamespace)
}

// SignSSH returns an armored SSHSIG signature (PROTOCOL.sshsig) over message.
func SignSSH(privateKey, message []byte, namespace string) (string, error) {
	signer, err := parseSSHKey(bytes.TrimSpace(privateKey))
	if err != nil {
		return "", err
	}
	if namespace == "" {
		return "", errors.New("coding: an SSH signature namespace is required")
	}
	const hashAlgorithm = "sha512"
	digest := sha512.Sum512(message)
	signedData := ssh.Marshal(struct {
		Namespace     string
		Reserved      string
		HashAlgorithm string
		Hash          string
	}{namespace, "", hashAlgorithm, string(digest[:])})
	signedData = append([]byte("SSHSIG"), signedData...)

	var signature *ssh.Signature
	if signer.PublicKey().Type() == ssh.KeyAlgoRSA {
		// ssh-rsa (SHA-1) signatures are rejected by ssh-keygen and GitHub.
		algorithmSigner, ok := signer.(ssh.AlgorithmSigner)
		if !ok {
			return "", fmt.Errorf("%w: RSA key cannot make SHA-512 signatures", ErrSigningKeyInvalid)
		}
		signature, err = algorithmSigner.SignWithAlgorithm(rand.Reader, signedData, ssh.KeyAlgoRSASHA512)
	} else {
		signature, err = signer.Sign(rand.Reader, signedData)
	}
	if err != nil {
		return "", fmt.Errorf("coding: ssh sign: %w", err)
	}
	blob := ssh.Marshal(struct {
		Version       uint32
		PublicKey     string
		Namespace     string
		Reserved      string
		HashAlgorithm string
		Signature     string
	}{1, string(signer.PublicKey().Marshal()), namespace, "", hashAlgorithm, string(ssh.Marshal(signature))})
	blob = append([]byte("SSHSIG"), blob...)
	return armorSSHSignature(blob), nil
}

// armorSSHSignature wraps the blob like ssh-keygen: base64 in 70-column lines.
func armorSSHSignature(blob []byte) string {
	encoded := base64.StdEncoding.EncodeToString(blob)
	var out strings.Builder
	out.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(encoded) > 70 {
		out.WriteString(encoded[:70])
		out.WriteByte('\n')
		encoded = encoded[70:]
	}
	out.WriteString(encoded)
	out.WriteString("\n-----END SSH SIGNATURE-----\n")
	return out.String()
}

// SignOpenPGP returns an armored binary detached signature over message, as
// `gpg --detach-sign --armor` makes for git.
func SignOpenPGP(privateKey, message []byte, now time.Time) (string, error) {
	entity, err := parseOpenPGPKey(bytes.TrimSpace(privateKey))
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	config := &packet.Config{DefaultHash: crypto.SHA256, Time: func() time.Time { return now }}
	if err := openpgp.ArmoredDetachSign(&out, entity, bytes.NewReader(message), config); err != nil {
		return "", fmt.Errorf("coding: openpgp sign: %w", err)
	}
	signature := out.String()
	if !strings.HasSuffix(signature, "\n") {
		signature += "\n"
	}
	return signature, nil
}

// LegacySigningFingerprint is the fingerprint the account API stored for
// OpenPGP keys before Flow kept key material ("SHA256:" + hex of the armored
// text). It lets callers recognise metadata from that era.
func LegacySigningFingerprint(raw []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(raw))
	return "SHA256:" + hex.EncodeToString(sum[:])
}

// CommitSigningError is a signing failure with the coding error code the
// session and the runner surface (missingCommitSigningKey or signingFailed).
type CommitSigningError struct {
	Code    domain.CodingErrorCode
	Message string
	Err     error
}

func (e *CommitSigningError) Error() string { return e.Message }

// Unwrap exposes the cause.
func (e *CommitSigningError) Unwrap() error { return e.Err }

// CodingErrorCode is the session error code.
func (e *CommitSigningError) CodingErrorCode() domain.CodingErrorCode { return e.Code }

// Messages shown with missingCommitSigningKey. The legacy message covers users
// whose key was uploaded before Flow stored private keys for signing: their
// account shows a key, but Flow has nothing to sign with until they upload it
// again.
const (
	MissingSigningKeyMessage = "Signed commits are required for coding sessions in this workspace. Upload a commit signing key (SSH or OpenPGP) in Settings › Account › Commit signing."
	LegacySigningKeyMessage  = "Your commit signing key was uploaded before Flow could sign commits on your behalf, so Flow has no copy of it. Remove it and upload it again in Settings › Account › Commit signing."
	UnreadableSigningKeyMsg  = "Flow could not read your stored commit signing key (the server's secret key may have changed). Upload it again in Settings › Account › Commit signing."
)

// SigningResolution is how a session's commits are signed.
type SigningResolution struct {
	// Format is "" (unsigned), SigningFormatSSH or SigningFormatOpenPGP;
	// it fills BootstrapGit.SigningFormat.
	Format string
	// PublicKey fills BootstrapGit.SigningPublicKey.
	PublicKey   string
	Fingerprint string
}

// ResolveCommitSigning decides how the session creator's commits are signed
// (provisioning, §4.1). When the workspace requires signed commits
// (CodingAgentSettings.CommitSigningEnabled) and the user has no usable stored
// key, it fails with missingCommitSigningKey; hasLegacyKey (the user's account
// shows CommitSigningKey metadata) selects the re-upload message. When signing
// is optional, a usable key still signs and a missing one leaves commits
// unsigned. Store failures are returned as they are (transient).
func ResolveCommitSigning(ctx context.Context, keys SigningKeyStore, userID string, required, hasLegacyKey bool) (SigningResolution, error) {
	if keys == nil {
		if required {
			return SigningResolution{}, &CommitSigningError{Code: domain.CodingErrMissingCommitSigningKey, Message: MissingSigningKeyMessage}
		}
		return SigningResolution{}, nil
	}
	_, material, err := keys.SigningKeyMaterial(ctx, userID)
	if signingKeyNotFound(err) {
		if !required {
			return SigningResolution{}, nil
		}
		message := MissingSigningKeyMessage
		if hasLegacyKey {
			message = LegacySigningKeyMessage
		}
		return SigningResolution{}, &CommitSigningError{Code: domain.CodingErrMissingCommitSigningKey, Message: message, Err: err}
	}
	if err != nil {
		if !signingKeyUnreadable(err) {
			return SigningResolution{}, err
		}
		if !required {
			return SigningResolution{}, nil
		}
		return SigningResolution{}, &CommitSigningError{Code: domain.CodingErrMissingCommitSigningKey, Message: UnreadableSigningKeyMsg, Err: err}
	}
	key, err := ParseSigningKey(material)
	if err != nil {
		if !required {
			return SigningResolution{}, nil
		}
		return SigningResolution{}, &CommitSigningError{Code: domain.CodingErrMissingCommitSigningKey, Message: UnreadableSigningKeyMsg, Err: err}
	}
	return SigningResolution{Format: key.Format, PublicKey: key.PublicKey, Fingerprint: key.Fingerprint}, nil
}

// SigningKeyStore implementations report a missing key with ErrNotFound or an
// error whose NotFound() method returns true, and an undecryptable key (the
// secret key changed, a corrupt row) with an error whose
// SigningKeyUnreadable() method returns true. The behaviour checks keep the
// store package free of an import of coding.
func signingKeyNotFound(err error) bool {
	if errors.Is(err, ErrNotFound) {
		return true
	}
	var notFound interface{ NotFound() bool }
	return errors.As(err, &notFound) && notFound.NotFound()
}

func signingKeyUnreadable(err error) bool {
	var unreadable interface{ SigningKeyUnreadable() bool }
	return errors.As(err, &unreadable) && unreadable.SigningKeyUnreadable()
}

// SignForUser signs a commit payload for the session creator (the /sign
// runtime endpoint). Missing keys fail with missingCommitSigningKey, other
// failures with signingFailed.
func SignForUser(ctx context.Context, keys SigningKeyStore, userID string, request SignRequest, hasLegacyKey bool) (SignResponse, error) {
	if len(request.Payload) == 0 {
		return SignResponse{}, &CommitSigningError{Code: domain.CodingErrSigningFailed, Message: "The payload to sign is empty."}
	}
	if len(request.Payload) > MaxSignPayloadBytes {
		return SignResponse{}, &CommitSigningError{Code: domain.CodingErrSigningFailed, Message: fmt.Sprintf("The payload to sign exceeds %d bytes.", MaxSignPayloadBytes)}
	}
	if request.Format != "" && request.Format != SigningFormatSSH && request.Format != SigningFormatOpenPGP {
		return SignResponse{}, &CommitSigningError{Code: domain.CodingErrSigningFailed, Message: "Unknown signing format " + request.Format + "."}
	}
	if keys == nil {
		return SignResponse{}, &CommitSigningError{Code: domain.CodingErrMissingCommitSigningKey, Message: MissingSigningKeyMessage}
	}
	format, material, err := keys.SigningKeyMaterial(ctx, userID)
	if signingKeyNotFound(err) {
		message := MissingSigningKeyMessage
		if hasLegacyKey {
			message = LegacySigningKeyMessage
		}
		return SignResponse{}, &CommitSigningError{Code: domain.CodingErrMissingCommitSigningKey, Message: message, Err: err}
	}
	if err != nil {
		if signingKeyUnreadable(err) {
			return SignResponse{}, &CommitSigningError{Code: domain.CodingErrMissingCommitSigningKey, Message: UnreadableSigningKeyMsg, Err: err}
		}
		return SignResponse{}, err
	}
	if request.Format != "" && format != "" && request.Format != format {
		return SignResponse{}, &CommitSigningError{Code: domain.CodingErrSigningFailed, Message: "git asked for a " + request.Format + " signature but your signing key is " + format + ".", Err: ErrSigningFormatMismatch}
	}
	signature, err := Sign(request.Format, material, request.Payload)
	if err != nil {
		code, message := domain.CodingErrSigningFailed, "Flow could not sign the commit with your key."
		if errors.Is(err, ErrSigningKeyInvalid) || errors.Is(err, ErrSigningKeyEncrypted) {
			code, message = domain.CodingErrMissingCommitSigningKey, UnreadableSigningKeyMsg
		}
		return SignResponse{}, &CommitSigningError{Code: code, Message: message, Err: err}
	}
	return SignResponse{Signature: signature}, nil
}
