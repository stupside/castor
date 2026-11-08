package airplay

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/chacha20poly1305"
)

// HAP TLV8 tags, as the HomeKit Accessory Protocol numbers them.
const (
	tlvMethod        = 0x00
	tlvIdentifier    = 0x01
	tlvSalt          = 0x02
	tlvPublicKey     = 0x03
	tlvProof         = 0x04
	tlvEncryptedData = 0x05
	tlvState         = 0x06
	tlvError         = 0x07
	tlvBackOff       = 0x08
	tlvSignature     = 0x0a
	tlvName          = 0x11
	tlvFlags         = 0x13
)

// flagTransient asks for a pairing that lasts one session, with no PIN shown.
const flagTransient = 0x10

type tlvItem struct {
	tag   byte
	value []byte
}

// encodeTLV writes items in order, splitting a value longer than 255 bytes over repeated tags.
func encodeTLV(items ...tlvItem) []byte {
	var out bytes.Buffer
	for _, it := range items {
		value := it.value
		for {
			n := min(len(value), 255)
			out.WriteByte(it.tag)
			out.WriteByte(byte(n))
			out.Write(value[:n])
			value = value[n:]
			if len(value) == 0 {
				break
			}
		}
	}
	return out.Bytes()
}

// decodeTLV reads a TLV8 body, joining the fragments of a tag that repeats.
func decodeTLV(data []byte) (map[byte][]byte, error) {
	out := map[byte][]byte{}
	for len(data) > 0 {
		if len(data) < 2 || len(data) < 2+int(data[1]) {
			return nil, errors.New("truncated TLV8 body")
		}
		tag, n := data[0], int(data[1])
		out[tag] = append(out[tag], data[2:2+n]...)
		data = data[2+n:]
	}
	return out, nil
}

// tlvFailure is the error a receiver's TLV8 answer reports, nil where it reports none.
func tlvFailure(t map[byte][]byte) error {
	code, ok := t[tlvError]
	if !ok {
		return nil
	}
	var c byte
	if len(code) > 0 {
		c = code[0]
	}
	switch c {
	case 0x02:
		return errWrongPIN
	case 0x03:
		return errors.New("the receiver asks to wait before pairing again")
	case 0x04:
		return errors.New("the receiver has no room for another paired client")
	case 0x05:
		return errors.New("the receiver refuses further pairing attempts for now")
	case 0x06:
		return errors.New("the receiver cannot pair right now")
	case 0x07:
		return errors.New("the receiver is pairing with another client")
	}
	return fmt.Errorf("the receiver refused pairing (HAP error %d)", c)
}

var errWrongPIN = errors.New("the PIN does not match the one the receiver shows")

// The SRP-6a group HAP pairs over: RFC 5054's 3072-bit prime with generator 5, hashed with SHA-512.
var (
	srpN = func() *big.Int {
		n, _ := new(big.Int).SetString(""+
			"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74"+
			"020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F1437"+
			"4FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED"+
			"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF05"+
			"98DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB"+
			"9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B"+
			"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718"+
			"3995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33"+
			"A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7"+
			"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864"+
			"D87602733EC86A64521F2B18177B200CBBE117577A615D6C770988C0BAD946E2"+
			"08E24FA074E5AB3143DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF", 16)
		return n
	}()
	srpG = big.NewInt(5)
)

const srpUser = "Pair-Setup"

func sha(parts ...[]byte) []byte {
	h := sha512.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// pad writes v at the prime's length, as SRP hashes A, B and g.
func pad(v *big.Int) []byte { return v.FillBytes(make([]byte, len(srpN.Bytes()))) }

// srpClient is the client half of one SRP-6a exchange, private value a.
type srpClient struct {
	a, A *big.Int
	// key and proof are the session key K and the client proof M1, set once the server's answer is read.
	key, proof []byte
}

func newSRPClient(private []byte) *srpClient {
	a := new(big.Int).SetBytes(private)
	return &srpClient{a: a, A: new(big.Int).Exp(srpG, a, srpN)}
}

// public is A as the receiver reads it.
func (c *srpClient) public() []byte { return c.A.Bytes() }

// answer derives K and M1 from the receiver's salt and public B, for the PIN pin.
func (c *srpClient) answer(salt, serverPublic []byte, pin string) error {
	B := new(big.Int).SetBytes(serverPublic)
	if new(big.Int).Mod(B, srpN).Sign() == 0 {
		return errors.New("the receiver sent an invalid SRP public key")
	}
	k := new(big.Int).SetBytes(sha(srpN.Bytes(), pad(srpG)))
	u := new(big.Int).SetBytes(sha(pad(c.A), pad(B)))
	if u.Sign() == 0 {
		return errors.New("the receiver sent an invalid SRP public key")
	}
	x := new(big.Int).SetBytes(sha(salt, sha([]byte(srpUser+":"+pin))))
	// S = (B - k*g^x) ^ (a + u*x) mod N
	base := new(big.Int).Sub(B, new(big.Int).Mul(k, new(big.Int).Exp(srpG, x, srpN)))
	base.Mod(base, srpN)
	exp := new(big.Int).Add(c.a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exp, srpN)
	c.key = sha(S.Bytes())

	hn, hg := new(big.Int).SetBytes(sha(srpN.Bytes())), new(big.Int).SetBytes(sha(srpG.Bytes()))
	c.proof = sha(new(big.Int).Xor(hn, hg).Bytes(), sha([]byte(srpUser)), salt, c.A.Bytes(), B.Bytes(), c.key)
	return nil
}

// verifies is whether the receiver's M2 proves it knew the same PIN.
func (c *srpClient) verifies(serverProof []byte) bool {
	return bytes.Equal(sha(c.A.Bytes(), c.proof, c.key), serverProof)
}

// derive is the 32-byte key HAP derives for one purpose from a shared secret.
func derive(secret []byte, salt, info string) []byte {
	key, err := hkdf.Key(sha512.New, secret, []byte(salt), info, 32)
	if err != nil {
		panic(err) // 32 bytes of SHA-512 HKDF cannot fail.
	}
	return key
}

// labelNonce is a pairing message's nonce: its 8-byte label after four zero bytes.
func labelNonce(label string) []byte {
	return append(make([]byte, 4), label...)
}

// counterNonce is a session frame's nonce: its little-endian counter after four zero bytes.
func counterNonce(n uint64) []byte {
	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce[4:], n)
	return nonce
}

func seal(key, nonce, plain, aad []byte) []byte {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		panic(err) // keys are always 32 bytes.
	}
	return aead.Seal(nil, nonce, plain, aad)
}

func unseal(key, nonce, sealed, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, sealed, aad)
}

// opackName is the opack encoding of {"name": name}, the one opack value pair-setup sends.
func opackName(name string) []byte {
	out := []byte{0xe1}
	for _, s := range []string{"name", name} {
		switch n := len(s); {
		case n < 0x20:
			out = append(out, 0x40+byte(n))
		case n <= 0xff:
			out = append(out, 0x61, byte(n))
		default:
			s = s[:0xff]
			out = append(out, 0x61, 0xff)
		}
		out = append(out, s...)
	}
	return out
}
