package airplay

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// Credentials are what pair-setup leaves behind: castor's identity on the receiver and the receiver's own.
// ClientSeed is a private key: it is stored as a secret and never logged.
type Credentials struct {
	ClientID    string `json:"client_id"`
	ClientSeed  []byte `json:"client_seed"`
	ReceiverID  []byte `json:"receiver_id"`
	ReceiverKey []byte `json:"receiver_key"`
}

func (c Credentials) valid() bool {
	return c.ClientID != "" && len(c.ClientSeed) == ed25519.SeedSize && len(c.ReceiverID) > 0 && len(c.ReceiverKey) == ed25519.PublicKeySize
}

// String keeps every key out of logs.
func (c Credentials) String() string { return "airplay credentials for client " + c.ClientID }

// newUUID is a random (version 4) UUID, as AirPlay names identities and items.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// transientPIN is the PIN a transient pairing proves, which no receiver shows.
const transientPIN = "3939"

func pairHeaders(hkp string) map[string]string {
	return map[string]string{"User-Agent": "AirPlay/320.20", "Connection": "keep-alive", "X-Apple-HKP": hkp}
}

// pairStep posts one TLV8 pairing message and reads the receiver's TLV8 answer.
func pairStep(ctx context.Context, l *link, path, hkp string, items ...tlvItem) (map[byte][]byte, error) {
	resp, err := l.exchange(ctx, request{method: "POST", uri: path, proto: protoHTTP, header: pairHeaders(hkp), contentType: contentTLV, body: encodeTLV(items...)})
	if err != nil {
		return nil, err
	}
	if err := ok(resp, path); err != nil {
		return nil, err
	}
	answer, err := decodeTLV(resp.body)
	if err != nil {
		return nil, fmt.Errorf("reading the receiver's answer to %s: %w", path, err)
	}
	if err := tlvFailure(answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// pairSetup is a PIN pairing in progress: the receiver shows its PIN from begin until finish.
type pairSetup struct {
	l          *link
	srp        *srpClient
	salt, peer []byte
}

// beginPairSetup asks the receiver to show a PIN, and reads the SRP parameters the PIN is proven against.
func beginPairSetup(ctx context.Context, l *link) (*pairSetup, error) {
	resp, err := l.exchange(ctx, request{method: "POST", uri: "/pair-pin-start", proto: protoHTTP, header: pairHeaders("3")})
	if err != nil {
		return nil, err
	}
	if err := ok(resp, "showing a PIN"); err != nil {
		return nil, err
	}
	m2, err := pairStep(ctx, l, "/pair-setup", "3", tlvItem{tlvMethod, []byte{0}}, tlvItem{tlvState, []byte{1}})
	if err != nil {
		return nil, err
	}
	private := make([]byte, 32)
	_, _ = rand.Read(private)
	p := &pairSetup{l: l, srp: newSRPClient(private), salt: m2[tlvSalt], peer: m2[tlvPublicKey]}
	if len(p.salt) == 0 || len(p.peer) == 0 {
		return nil, errors.New("the receiver started pairing without its SRP salt and public key")
	}
	return p, nil
}

// finish proves pin, exchanges long-term keys and returns the credentials castor verifies with from now on.
func (p *pairSetup) finish(ctx context.Context, pin, name string) (Credentials, error) {
	if err := p.srp.answer(p.salt, p.peer, pin); err != nil {
		return Credentials{}, err
	}
	m4, err := pairStep(ctx, p.l, "/pair-setup", "3", tlvItem{tlvState, []byte{3}}, tlvItem{tlvPublicKey, p.srp.public()}, tlvItem{tlvProof, p.srp.proof})
	if err != nil {
		return Credentials{}, err
	}
	if !p.srp.verifies(m4[tlvProof]) {
		return Credentials{}, errors.New("the receiver could not prove it knows the PIN")
	}

	seed := make([]byte, ed25519.SeedSize)
	_, _ = rand.Read(seed)
	signer := ed25519.NewKeyFromSeed(seed)
	public := signer.Public().(ed25519.PublicKey)
	id := newUUID()
	controllerX := derive(p.srp.key, "Pair-Setup-Controller-Sign-Salt", "Pair-Setup-Controller-Sign-Info")
	key := derive(p.srp.key, "Pair-Setup-Encrypt-Salt", "Pair-Setup-Encrypt-Info")
	signed := ed25519.Sign(signer, bytes.Join([][]byte{controllerX, []byte(id), public}, nil))
	sub := encodeTLV(tlvItem{tlvIdentifier, []byte(id)}, tlvItem{tlvPublicKey, public}, tlvItem{tlvSignature, signed}, tlvItem{tlvName, opackName(name)})
	m6, err := pairStep(ctx, p.l, "/pair-setup", "3", tlvItem{tlvState, []byte{5}}, tlvItem{tlvEncryptedData, seal(key, labelNonce("PS-Msg05"), sub, nil)})
	if err != nil {
		return Credentials{}, err
	}
	plain, err := unseal(key, labelNonce("PS-Msg06"), m6[tlvEncryptedData], nil)
	if err != nil {
		return Credentials{}, fmt.Errorf("the receiver's pairing answer does not decrypt: %w", err)
	}
	theirs, err := decodeTLV(plain)
	if err != nil {
		return Credentials{}, err
	}
	receiverID, receiverKey := theirs[tlvIdentifier], theirs[tlvPublicKey]
	if len(receiverID) == 0 || len(receiverKey) != ed25519.PublicKeySize {
		return Credentials{}, errors.New("the receiver paired without its identity")
	}
	accessoryX := derive(p.srp.key, "Pair-Setup-Accessory-Sign-Salt", "Pair-Setup-Accessory-Sign-Info")
	if !ed25519.Verify(receiverKey, bytes.Join([][]byte{accessoryX, receiverID, receiverKey}, nil), theirs[tlvSignature]) {
		return Credentials{}, errors.New("the receiver's identity is not signed by its key")
	}
	return Credentials{ClientID: id, ClientSeed: seed, ReceiverID: receiverID, ReceiverKey: receiverKey}, nil
}

// errRejected is a receiver no longer accepting the credentials castor holds for it.
var errRejected = fmt.Errorf("the receiver no longer accepts castor's pairing: %w", device.ErrPairingRequired)

// verifyPairing proves to the receiver that castor holds creds, and returns the secret the session keys come from.
func verifyPairing(ctx context.Context, l *link, creds Credentials) ([]byte, error) {
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	mine := ephemeral.PublicKey().Bytes()
	m2, err := pairStep(ctx, l, "/pair-verify", "3", tlvItem{tlvState, []byte{1}}, tlvItem{tlvPublicKey, mine})
	if err != nil {
		if errors.Is(err, errWrongPIN) {
			return nil, errRejected
		}
		return nil, err
	}
	peer, err := ecdh.X25519().NewPublicKey(m2[tlvPublicKey])
	if err != nil {
		return nil, fmt.Errorf("the receiver sent an invalid verification key: %w", err)
	}
	shared, err := ephemeral.ECDH(peer)
	if err != nil {
		return nil, err
	}
	key := derive(shared, "Pair-Verify-Encrypt-Salt", "Pair-Verify-Encrypt-Info")
	plain, err := unseal(key, labelNonce("PV-Msg02"), m2[tlvEncryptedData], nil)
	if err != nil {
		return nil, fmt.Errorf("the receiver's verification does not decrypt: %w", err)
	}
	theirs, err := decodeTLV(plain)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(theirs[tlvIdentifier], creds.ReceiverID) {
		return nil, errRejected
	}
	if !ed25519.Verify(creds.ReceiverKey, bytes.Join([][]byte{m2[tlvPublicKey], creds.ReceiverID, mine}, nil), theirs[tlvSignature]) {
		return nil, errors.New("the receiver's verification is not signed by the key it paired with")
	}
	signer := ed25519.NewKeyFromSeed(creds.ClientSeed)
	signed := ed25519.Sign(signer, bytes.Join([][]byte{mine, []byte(creds.ClientID), m2[tlvPublicKey]}, nil))
	sub := encodeTLV(tlvItem{tlvIdentifier, []byte(creds.ClientID)}, tlvItem{tlvSignature, signed})
	if _, err := pairStep(ctx, l, "/pair-verify", "3", tlvItem{tlvState, []byte{3}}, tlvItem{tlvEncryptedData, seal(key, labelNonce("PV-Msg03"), sub, nil)}); err != nil {
		if errors.Is(err, errWrongPIN) {
			return nil, errRejected
		}
		return nil, err
	}
	return shared, nil
}

// pairTransient runs the PIN-less pairing a receiver allowing anyone on the network accepts for one session.
func pairTransient(ctx context.Context, l *link) ([]byte, error) {
	resp, err := l.exchange(ctx, request{method: "POST", uri: "/pair-pin-start", proto: protoHTTP, header: pairHeaders("4")})
	if err != nil {
		return nil, err
	}
	if err := ok(resp, "starting a transient pairing"); err != nil {
		return nil, err
	}
	m2, err := pairStep(ctx, l, "/pair-setup", "4", tlvItem{tlvMethod, []byte{0}}, tlvItem{tlvState, []byte{1}}, tlvItem{tlvFlags, []byte{flagTransient}})
	if err != nil {
		return nil, err
	}
	private := make([]byte, 32)
	_, _ = rand.Read(private)
	srp := newSRPClient(private)
	if err := srp.answer(m2[tlvSalt], m2[tlvPublicKey], transientPIN); err != nil {
		return nil, err
	}
	m4, err := pairStep(ctx, l, "/pair-setup", "4", tlvItem{tlvState, []byte{3}}, tlvItem{tlvPublicKey, srp.public()}, tlvItem{tlvProof, srp.proof})
	if err != nil {
		if errors.Is(err, errWrongPIN) {
			return nil, fmt.Errorf("the receiver refuses transient pairing: %w", device.ErrPairingRequired)
		}
		return nil, err
	}
	if !srp.verifies(m4[tlvProof]) {
		return nil, errors.New("the receiver could not prove the transient pairing")
	}
	return srp.key, nil
}

// controlKeys are the keys the control connection is sealed with, castor's writing key first.
func controlKeys(secret []byte) (write, read []byte) {
	return derive(secret, "Control-Salt", "Control-Write-Encryption-Key"), derive(secret, "Control-Salt", "Control-Read-Encryption-Key")
}

// eventKeys are the event channel's, which the receiver opens: castor writes with what it reads with.
func eventKeys(secret []byte) (write, read []byte) {
	return derive(secret, "Events-Salt", "Events-Read-Encryption-Key"), derive(secret, "Events-Salt", "Events-Write-Encryption-Key")
}
