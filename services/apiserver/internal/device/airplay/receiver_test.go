package airplay

import (
	"bufio"
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"math"
	"math/big"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"howett.net/plist"
)

// fakeReceiver answers castor as an AirPlay receiver does, from what an LG C2 and pyatv's Apple TV path were seen to send.
type fakeReceiver struct {
	t      *testing.T
	ln     net.Listener
	events net.Listener

	info       map[string]any
	receiverID []byte
	receiver   ed25519.PrivateKey
	client     Credentials
	pin        string

	// script is what a video v2 receiver reports once castor sets the rate to play, ITEM standing for the item queued.
	script []map[string]any
	// plays answers each /play in turn, then 200; infos are /playback-info's answers in turn, the last repeated.
	plays []int
	infos []map[string]any

	mu        sync.Mutex
	requests  []string
	commands  []map[string]any
	item      string
	queued    bool
	lastInfo  map[string]any
	eventConn net.Conn
	eventSeq  int
	infoAt    int
	closed    chan struct{}
	eventKeys [2][]byte
}

func newFakeReceiver(t *testing.T, features uint64, status uint64) *fakeReceiver {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	events, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, receiver, _ := ed25519.GenerateKey(rand.Reader)
	seed := make([]byte, ed25519.SeedSize)
	_, _ = rand.Read(seed)
	r := &fakeReceiver{
		t: t, ln: ln, events: events, receiver: receiver, receiverID: []byte("C2-RECEIVER"),
		info:   map[string]any{"deviceID": "DC:00:00:00:00:01", "features": features, "statusFlags": status, "model": "OLED42C24LA", "name": "[LG] webOS TV"},
		closed: make(chan struct{}),
	}
	r.client = Credentials{ClientID: "client-1", ClientSeed: seed, ReceiverID: r.receiverID, ReceiverKey: receiver.Public().(ed25519.PublicKey)}
	t.Cleanup(func() { _ = ln.Close(); _ = events.Close() })
	go r.accept()
	go r.acceptEvents()
	return r
}

func (r *fakeReceiver) address() string { return r.ln.Addr().String() }

func (r *fakeReceiver) saw(request string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.requests, request)
}

func (r *fakeReceiver) sent(kind string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, c := range r.commands {
		if c["type"] == kind {
			out = append(out, c)
		}
	}
	return out
}

func (r *fakeReceiver) accept() {
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			return
		}
		go r.serve(conn)
	}
}

func (r *fakeReceiver) acceptEvents() {
	for {
		conn, err := r.events.Accept()
		if err != nil {
			return
		}
		r.mu.Lock()
		keys := r.eventKeys
		r.mu.Unlock()
		s := secure(conn, nil, keys[0], keys[1])
		r.mu.Lock()
		r.eventConn = s
		r.mu.Unlock()
		go func() {
			reader := bufio.NewReader(s)
			for {
				if _, err := readMessage(reader); err != nil {
					return
				}
			}
		}()
	}
}

func (r *fakeReceiver) answer(w net.Conn, req *message, code int, body []byte) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %d X\r\nCSeq: %s\r\nContent-Length: %d\r\n", req.proto, code, req.header.Get("CSeq"), len(body))
	if body != nil {
		b.WriteString("Content-Type: " + contentBplist + "\r\n")
	}
	b.WriteString("\r\n")
	b.Write(body)
	_, _ = w.Write(b.Bytes())
}

func mustPlist(t *testing.T, v any) []byte {
	b, err := plist.Marshal(v, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (r *fakeReceiver) serve(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		select {
		case <-r.closed:
		default:
			close(r.closed)
		}
	}()
	rw := conn
	reader := bufio.NewReader(conn)
	var verifyKey, eph []byte
	var serverEph *ecdh.PrivateKey
	var setup *pairServer
	for {
		req, err := readMessage(reader)
		if err != nil {
			return
		}
		r.mu.Lock()
		r.requests = append(r.requests, req.method+" "+req.uri)
		r.mu.Unlock()
		switch {
		case req.method == "GET" && req.uri == "/info":
			r.answer(rw, req, 200, mustPlist(r.t, r.info))
		case req.uri == "/pair-pin-start":
			r.answer(rw, req, 200, nil)
		case req.uri == "/pair-setup":
			if setup == nil {
				setup = newPairServer(r.pin)
			}
			body := setup.step(r, req.body)
			r.answer(rw, req, 200, body)
		case req.uri == "/pair-verify":
			in, _ := decodeTLV(req.body)
			switch in[tlvState][0] {
			case 1:
				if r.pin == "reject" {
					r.answer(rw, req, 200, encodeTLV(tlvItem{tlvState, []byte{2}}, tlvItem{tlvError, []byte{2}}))
					continue
				}
				eph = in[tlvPublicKey]
				serverEph, _ = ecdh.X25519().GenerateKey(rand.Reader)
				peer, _ := ecdh.X25519().NewPublicKey(eph)
				shared, _ := serverEph.ECDH(peer)
				verifyKey = shared
				key := derive(shared, "Pair-Verify-Encrypt-Salt", "Pair-Verify-Encrypt-Info")
				mine := serverEph.PublicKey().Bytes()
				signed := ed25519.Sign(r.receiver, bytes.Join([][]byte{mine, r.receiverID, eph}, nil))
				sub := encodeTLV(tlvItem{tlvIdentifier, r.receiverID}, tlvItem{tlvSignature, signed})
				r.answer(rw, req, 200, encodeTLV(tlvItem{tlvState, []byte{2}}, tlvItem{tlvPublicKey, mine}, tlvItem{tlvEncryptedData, seal(key, labelNonce("PV-Msg02"), sub, nil)}))
			case 3:
				key := derive(verifyKey, "Pair-Verify-Encrypt-Salt", "Pair-Verify-Encrypt-Info")
				plain, err := unseal(key, labelNonce("PV-Msg03"), in[tlvEncryptedData], nil)
				theirs, _ := decodeTLV(plain)
				clientKey := ed25519.NewKeyFromSeed(r.client.ClientSeed).Public().(ed25519.PublicKey)
				if err != nil || string(theirs[tlvIdentifier]) != r.client.ClientID ||
					!ed25519.Verify(clientKey, bytes.Join([][]byte{eph, theirs[tlvIdentifier], serverEph.PublicKey().Bytes()}, nil), theirs[tlvSignature]) {
					r.answer(rw, req, 200, encodeTLV(tlvItem{tlvState, []byte{4}}, tlvItem{tlvError, []byte{2}}))
					continue
				}
				r.answer(rw, req, 200, encodeTLV(tlvItem{tlvState, []byte{4}}))
				// From here the control connection is sealed, the receiver writing with what castor reads.
				write, read := derive(verifyKey, "Control-Salt", "Control-Read-Encryption-Key"), derive(verifyKey, "Control-Salt", "Control-Write-Encryption-Key")
				s := secure(conn, reader, write, read)
				rw, reader = s, bufio.NewReader(s)
				r.mu.Lock()
				r.eventKeys = [2][]byte{derive(verifyKey, "Events-Salt", "Events-Write-Encryption-Key"), derive(verifyKey, "Events-Salt", "Events-Read-Encryption-Key")}
				r.mu.Unlock()
			}
		case req.method == "SETUP":
			var body map[string]any
			_, _ = plist.Unmarshal(req.body, &body)
			if _, ok := body["streams"]; ok {
				r.answer(rw, req, 200, mustPlist(r.t, map[string]any{"streams": []any{map[string]any{"streamID": 7, "type": 130}}}))
			} else {
				port := r.events.Addr().(*net.TCPAddr).Port
				r.answer(rw, req, 200, mustPlist(r.t, map[string]any{"eventPort": port, "timingPort": 0}))
			}
		case req.method == "POST" && req.uri == "/command":
			command, err := decodeCommand(req.body)
			if err != nil {
				r.answer(rw, req, 400, nil)
				continue
			}
			r.answer(rw, req, 200, nil)
			r.command(command)
		case req.method == "POST" && req.uri == "/play":
			code := 200
			r.mu.Lock()
			if len(r.plays) > 0 {
				code, r.plays = r.plays[0], r.plays[1:]
			}
			r.mu.Unlock()
			r.answer(rw, req, code, nil)
		case req.method == "GET" && req.uri == "/playback-info":
			r.mu.Lock()
			var info map[string]any
			if len(r.infos) > 0 {
				info = r.infos[min(r.infoAt, len(r.infos)-1)]
				r.infoAt++
			}
			r.mu.Unlock()
			if info == nil {
				info = map[string]any{}
			}
			r.answer(rw, req, 200, mustPlist(r.t, info))
		default:
			r.answer(rw, req, 200, nil)
		}
	}
}

func (r *fakeReceiver) command(c map[string]any) {
	r.mu.Lock()
	r.commands = append(r.commands, c)
	switch c["type"] {
	case "insertPlayQueueItem":
		if item, ok := c["item"].(map[string]any); ok {
			r.item, _ = item["uuid"].(string)
			r.queued = true
		}
	case "setRate":
		// Each queued item plays the script once; a later rate change is a pause or a resume.
		if rate, _ := number(c["rate"]); rate == 1 && r.queued {
			script := r.script
			r.queued = false
			r.mu.Unlock()
			go func() {
				for _, ev := range script {
					r.emit(ev)
					time.Sleep(5 * time.Millisecond)
				}
			}()
			return
		}
	case "playbackInfo":
		id := c["messageID"]
		info := r.lastInfo
		r.mu.Unlock()
		if info == nil {
			info = map[string]any{"playbackState": "loading"}
		}
		r.emit(map[string]any{"type": "playbackInfo", "kind": "response", "messageID": id, "info": info})
		return
	}
	r.mu.Unlock()
}

// emit sends ev on the event channel, as a request castor answers, ITEM replaced by the item castor queued.
func (r *fakeReceiver) emit(ev map[string]any) {
	r.mu.Lock()
	ev = withItem(ev, r.item).(map[string]any)
	if info, ok := ev["info"].(map[string]any); ok {
		r.lastInfo = info
	}
	conn := r.eventConn
	r.eventSeq++
	seq := r.eventSeq
	r.mu.Unlock()
	if conn == nil {
		return
	}
	body, err := encodeCommand(ev)
	if err != nil {
		r.t.Error(err)
		return
	}
	_, _ = fmt.Fprintf(conn, "POST /command RTSP/1.0\r\nCSeq: %d\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n%s", seq, contentBplist, len(body), body)
}

// withItem copies v with ITEM replaced by item, and JSON's whole floats as the integers the receiver sends.
func withItem(v any, item string) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = withItem(e, item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = withItem(e, item)
		}
		return out
	case string:
		if x == "ITEM" {
			return item
		}
	case float64:
		if x == math.Trunc(x) {
			return int64(x)
		}
	}
	return v
}

// scenario is one sequence an LG C2 reported, from testdata.
func scenario(t *testing.T, name string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/lg_c2_events.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Scenarios map[string][]map[string]any `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Scenarios[name]
	if !ok {
		t.Fatalf("no scenario %q", name)
	}
	return s
}

// pairServer is the receiver's half of a PIN pair-setup.
type pairServer struct {
	pin     string
	salt    []byte
	b, B, v *big.Int
	key     []byte
}

func newPairServer(pin string) *pairServer {
	p := &pairServer{pin: pin, salt: make([]byte, 16)}
	_, _ = rand.Read(p.salt)
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	p.b = new(big.Int).SetBytes(b)
	x := new(big.Int).SetBytes(sha(p.salt, sha([]byte(srpUser+":"+pin))))
	p.v = new(big.Int).Exp(srpG, x, srpN)
	k := new(big.Int).SetBytes(sha(srpN.Bytes(), pad(srpG)))
	p.B = new(big.Int).Add(new(big.Int).Mul(k, p.v), new(big.Int).Exp(srpG, p.b, srpN))
	p.B.Mod(p.B, srpN)
	return p
}

func (p *pairServer) step(r *fakeReceiver, body []byte) []byte {
	in, _ := decodeTLV(body)
	switch in[tlvState][0] {
	case 1:
		return encodeTLV(tlvItem{tlvState, []byte{2}}, tlvItem{tlvSalt, p.salt}, tlvItem{tlvPublicKey, p.B.Bytes()})
	case 3:
		A := new(big.Int).SetBytes(in[tlvPublicKey])
		u := new(big.Int).SetBytes(sha(pad(A), pad(p.B)))
		S := new(big.Int).Exp(new(big.Int).Mul(A, new(big.Int).Exp(p.v, u, srpN)), p.b, srpN)
		p.key = sha(S.Bytes())
		hn, hg := new(big.Int).SetBytes(sha(srpN.Bytes())), new(big.Int).SetBytes(sha(srpG.Bytes()))
		want := sha(new(big.Int).Xor(hn, hg).Bytes(), sha([]byte(srpUser)), p.salt, A.Bytes(), p.B.Bytes(), p.key)
		if !bytes.Equal(want, in[tlvProof]) {
			return encodeTLV(tlvItem{tlvState, []byte{4}}, tlvItem{tlvError, []byte{2}})
		}
		return encodeTLV(tlvItem{tlvState, []byte{4}}, tlvItem{tlvProof, sha(A.Bytes(), in[tlvProof], p.key)})
	case 5:
		key := derive(p.key, "Pair-Setup-Encrypt-Salt", "Pair-Setup-Encrypt-Info")
		plain, err := unseal(key, labelNonce("PS-Msg05"), in[tlvEncryptedData], nil)
		if err != nil {
			return encodeTLV(tlvItem{tlvState, []byte{6}}, tlvItem{tlvError, []byte{2}})
		}
		theirs, _ := decodeTLV(plain)
		x := derive(p.key, "Pair-Setup-Controller-Sign-Salt", "Pair-Setup-Controller-Sign-Info")
		if !ed25519.Verify(theirs[tlvPublicKey], bytes.Join([][]byte{x, theirs[tlvIdentifier], theirs[tlvPublicKey]}, nil), theirs[tlvSignature]) {
			return encodeTLV(tlvItem{tlvState, []byte{6}}, tlvItem{tlvError, []byte{2}})
		}
		accessoryX := derive(p.key, "Pair-Setup-Accessory-Sign-Salt", "Pair-Setup-Accessory-Sign-Info")
		mine := r.receiver.Public().(ed25519.PublicKey)
		signed := ed25519.Sign(r.receiver, bytes.Join([][]byte{accessoryX, r.receiverID, mine}, nil))
		sub := encodeTLV(tlvItem{tlvIdentifier, r.receiverID}, tlvItem{tlvPublicKey, mine}, tlvItem{tlvSignature, signed})
		return encodeTLV(tlvItem{tlvState, []byte{6}}, tlvItem{tlvEncryptedData, seal(key, labelNonce("PS-Msg06"), sub, nil)})
	}
	return encodeTLV(tlvItem{tlvError, []byte{1}})
}

// Features as the LG C2 announces them, and as an Apple TV with video v1 does.
const (
	lgFeatures      = 0xB8BCF46<<32 | 0x7F8AD0
	lgStatus        = 0x244
	appleTVFeatures = 1<<featureVideoV1 | 1<<9
)

var _ = strconv.Itoa
var _ = strings.Contains
