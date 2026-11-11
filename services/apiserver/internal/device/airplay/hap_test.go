package airplay

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"math/big"
	"net"
	"os"
	"testing"
)

// vectors are pyatv's own outputs (srptools, cryptography, its HAPSession) for fixed inputs: castor must agree byte for byte.
type vectors struct {
	SRP struct {
		Private string `json:"a"`
		Salt    string `json:"salt"`
		Pin     string `json:"pin"`
		A       string `json:"A"`
		B       string `json:"B"`
		K       string `json:"K"`
		M1      string `json:"M1"`
		M2      string `json:"M2"`
	} `json:"srp"`
	HKDF struct {
		IKM   string `json:"ikm"`
		Cases []struct {
			Salt string `json:"salt"`
			Info string `json:"info"`
			Out  string `json:"out"`
		} `json:"cases"`
	} `json:"hkdf"`
	Chacha struct {
		Key   string `json:"key"`
		Plain string `json:"plain"`
		PV2   string `json:"pv2"`
	} `json:"chacha"`
	Framing struct {
		Out      string `json:"out"`
		In       string `json:"in"`
		PlainLen int    `json:"plain_len"`
		Frames   string `json:"frames"`
	} `json:"framing"`
	TLV struct {
		Out string `json:"out"`
	} `json:"tlv"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/hap_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSRPAgreesWithPyatv(t *testing.T) {
	v := loadVectors(t).SRP
	c := newSRPClient(unhex(t, v.Private))
	if got := hex.EncodeToString(c.public()); got != v.A {
		t.Fatalf("A = %s…, want %s…", got[:16], v.A[:16])
	}
	if err := c.answer(unhex(t, v.Salt), unhex(t, v.B), v.Pin); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(c.key); got != v.K {
		t.Errorf("K = %s, want %s", got, v.K)
	}
	if got := hex.EncodeToString(c.proof); got != v.M1 {
		t.Errorf("M1 = %s, want %s", got, v.M1)
	}
	if !c.verifies(unhex(t, v.M2)) {
		t.Error("the server's M2 does not verify")
	}
	wrong := newSRPClient(unhex(t, v.Private))
	if err := wrong.answer(unhex(t, v.Salt), unhex(t, v.B), "0428"); err != nil {
		t.Fatal(err)
	}
	if wrong.verifies(unhex(t, v.M2)) {
		t.Error("a wrong PIN verifies the server's proof")
	}
}

func TestSRPRefusesAServerKeyOutsideTheGroup(t *testing.T) {
	width := len(srpN.Bytes())
	for _, tt := range []struct {
		name string
		key  []byte
	}{
		{"zero", []byte{0}},
		{"nothing", nil},
		{"the modulus, which pins the session key", srpN.Bytes()},
		{"the modulus plus one", new(big.Int).Add(srpN, big.NewInt(1)).Bytes()},
		{"twice the modulus", new(big.Int).Lsh(srpN, 1).Bytes()},
		{"one byte wider than the group", append([]byte{1}, make([]byte, width)...)},
		{"far wider than the group", bytes.Repeat([]byte{0xff}, 4*width)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("a key the receiver sent before proving anything panicked instead of being refused: %v", r)
				}
			}()
			c := newSRPClient([]byte{1})
			if err := c.answer([]byte{1}, tt.key, "1234"); err == nil {
				t.Error("a server key outside the group was accepted")
			}
			if c.key != nil || c.proof != nil {
				t.Error("a refused server key still left a session key or a proof")
			}
		})
	}
}

func TestSRPTakesTheLargestServerKeyInTheGroup(t *testing.T) {
	c := newSRPClient([]byte{1})
	if err := c.answer([]byte{1}, new(big.Int).Sub(srpN, big.NewInt(1)).Bytes(), "1234"); err != nil {
		t.Errorf("B = N-1 was refused: %v", err)
	}
}

func TestKeysAgreeWithPyatv(t *testing.T) {
	v := loadVectors(t).HKDF
	for _, c := range v.Cases {
		if got := hex.EncodeToString(derive(unhex(t, v.IKM), c.Salt, c.Info)); got != c.Out {
			t.Errorf("%s/%s = %s, want %s", c.Salt, c.Info, got, c.Out)
		}
	}
}

func TestPairingMessagesAreSealedAsPyatvSealsThem(t *testing.T) {
	v := loadVectors(t).Chacha
	if got := hex.EncodeToString(seal(unhex(t, v.Key), labelNonce("PV-Msg02"), unhex(t, v.Plain), nil)); got != v.PV2 {
		t.Errorf("sealed = %s, want %s", got, v.PV2)
	}
}

func TestFramesAgreeWithPyatvAndReadBack(t *testing.T) {
	v := loadVectors(t).Framing
	plain := make([]byte, v.PlainLen)
	for i := range plain {
		plain[i] = byte(i % 251)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _, _ = secure(client, nil, unhex(t, v.Out), unhex(t, v.In)).Write(plain) }()
	frames := make([]byte, len(v.Frames)/2)
	if _, err := io.ReadFull(server, frames); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(frames); got != v.Frames {
		t.Fatalf("frames differ from pyatv's (%d bytes)", len(frames))
	}
	// The receiver's side reads them back with the keys swapped.
	reader := secure(nil, bufio.NewReader(bytes.NewReader(frames)), unhex(t, v.In), unhex(t, v.Out))
	got, err := io.ReadAll(reader)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("read back %d bytes, want the %d written", len(got), len(plain))
	}
}

func TestTLVSplitsLongValuesAsPyatv(t *testing.T) {
	v := loadVectors(t).TLV
	long := make([]byte, 300)
	for i := range long {
		long[i] = byte(i)
	}
	got := encodeTLV(tlvItem{tlvState, []byte{3}}, tlvItem{tlvPublicKey, long})
	if hex.EncodeToString(got) != v.Out {
		t.Fatalf("TLV = %x, want %s", got, v.Out)
	}
	back, err := decodeTLV(got)
	if err != nil || !bytes.Equal(back[tlvPublicKey], long) || !bytes.Equal(back[tlvState], []byte{3}) {
		t.Errorf("decoded %v, %v", back, err)
	}
	if _, err := decodeTLV([]byte{tlvSalt, 4, 1}); err == nil {
		t.Error("a truncated TLV was accepted")
	}
}

func TestOpackNameIsWhatPyatvPacks(t *testing.T) {
	// opack.pack({"name": "Castor"}) in pyatv.
	if got := hex.EncodeToString(opackName("Castor")); got != "e1446e616d6546436173746f72" {
		t.Errorf("opack = %s", got)
	}
}
