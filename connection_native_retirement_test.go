// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/polymorfa/libsignal-protocol-go/ecc"
	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waCert"
	"github.com/polymorfa/hypermeow/proto/waWa6"
	"github.com/polymorfa/hypermeow/socket"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/util/keys"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

// nativeWriteGate uses a real loopback TCP connection. Once armed, it blocks
// the native writer until the underlying connection is physically closed.
// Cancellation of the per-request context alone cannot satisfy this barrier.
type nativeWriteGate struct {
	net.Conn
	armed                            atomic.Bool
	entered, settled, closed         chan struct{}
	enterOnce, settleOnce, closeOnce sync.Once
}

func (g *nativeWriteGate) Write(p []byte) (int, error) {
	if g.armed.Load() {
		g.enterOnce.Do(func() { close(g.entered) })
		<-g.closed
		g.settleOnce.Do(func() { close(g.settled) })
		return 0, net.ErrClosed
	}
	return g.Conn.Write(p)
}
func (g *nativeWriteGate) Close() error {
	g.closeOnce.Do(func() { close(g.closed) })
	return g.Conn.Close()
}

func nativeCertificate(t *testing.T, root, intermediate, static *keys.KeyPair) []byte {
	t.Helper()
	detail := func(serial, issuer uint32, key []byte) []byte {
		raw, err := proto.Marshal(&waCert.CertChain_NoiseCertificate_Details{Serial: proto.Uint32(serial), IssuerSerial: proto.Uint32(issuer), Key: key, NotBefore: proto.Uint64(uint64(time.Now().Add(-time.Hour).Unix())), NotAfter: proto.Uint64(uint64(time.Now().Add(time.Hour).Unix()))})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	mid, leaf := detail(1, 0, intermediate.Pub[:]), detail(2, 1, static.Pub[:])
	sign := func(key *keys.KeyPair, raw []byte) []byte {
		sig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*key.Priv), raw)
		return sig[:]
	}
	raw, err := proto.Marshal(&waCert.CertChain{Intermediate: &waCert.CertChain_NoiseCertificate{Details: mid, Signature: sign(root, mid)}, Leaf: &waCert.CertChain_NoiseCertificate{Details: leaf, Signature: sign(intermediate, leaf)}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// nativeRetirementClient completes real Noise XX using a synthetic local CA.
// The peer then intentionally never reads or responds to a close handshake.
func nativeRetirementClient(t *testing.T) (*Client, *nativeWriteGate, context.CancelFunc) {
	t.Helper()
	root, mid, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	cert := nativeCertificate(t, root, mid, static)
	finish, serverStop := make(chan error, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			finish <- err
			return
		}
		defer ws.CloseNow()
		read := func(header bool) (*waWa6.HandshakeMessage, error) {
			_, raw, err := ws.Read(r.Context())
			if err != nil {
				return nil, err
			}
			if header {
				if len(raw) < 4 {
					return nil, io.ErrUnexpectedEOF
				}
				raw = raw[4:]
			}
			if len(raw) < 3 {
				return nil, io.ErrUnexpectedEOF
			}
			n := int(raw[0])<<16 | int(raw[1])<<8 | int(raw[2])
			if n != len(raw)-3 {
				return nil, errors.New("fixture frame length")
			}
			var msg waWa6.HandshakeMessage
			err = proto.Unmarshal(raw[3:], &msg)
			return &msg, err
		}
		hello, err := read(true)
		if err != nil {
			finish <- err
			return
		}
		clientEphemeral := hello.GetClientHello().GetEphemeral()
		if len(clientEphemeral) != 32 {
			finish <- errors.New("fixture ephemeral")
			return
		}
		ephem := keys.NewKeyPair()
		nh := socket.NewNoiseHandshake()
		nh.Start(socket.NoiseStartPattern, socket.WAConnHeader)
		nh.Authenticate(clientEphemeral)
		nh.Authenticate(ephem.Pub[:])
		if err = nh.MixSharedSecretIntoKey(*ephem.Priv, [32]byte(clientEphemeral)); err != nil {
			finish <- err
			return
		}
		encryptedStatic := nh.Encrypt(static.Pub[:])
		if err = nh.MixSharedSecretIntoKey(*static.Priv, [32]byte(clientEphemeral)); err != nil {
			finish <- err
			return
		}
		raw, _ := proto.Marshal(&waWa6.HandshakeMessage{ServerHello: &waWa6.HandshakeMessage_ServerHello{Ephemeral: ephem.Pub[:], Static: encryptedStatic, Payload: nh.Encrypt(cert)}})
		framed := make([]byte, len(raw)+3)
		binary.BigEndian.PutUint16(framed[1:3], uint16(len(raw)))
		copy(framed[3:], raw)
		if err = ws.Write(r.Context(), websocket.MessageBinary, framed); err != nil {
			finish <- err
			return
		}
		final, err := read(false)
		if err != nil {
			finish <- err
			return
		}
		clientStatic, err := nh.Decrypt(final.GetClientFinish().GetStatic())
		if err != nil || len(clientStatic) != 32 {
			finish <- errors.New("fixture client static")
			return
		}
		if err = nh.MixSharedSecretIntoKey(*ephem.Priv, [32]byte(clientStatic)); err != nil {
			finish <- err
			return
		}
		if _, err = nh.Decrypt(final.GetClientFinish().GetPayload()); err != nil {
			finish <- err
			return
		}
		finish <- nil
		<-serverStop
	}))
	t.Cleanup(func() { close(serverStop); server.Close() })
	var gate *nativeWriteGate
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		gate = &nativeWriteGate{Conn: c, entered: make(chan struct{}), settled: make(chan struct{}), closed: make(chan struct{})}
		return gate, nil
	}}
	client := NewRemoteSignalClient(&store.Device{NoiseKey: keys.NewKeyPair()}, testRemoteIdentity, &fakeRemoteSignal{}, waLog.Noop)
	client.EnableAutoReconnect = false
	client.SocketConfig = &SocketConfig{URL: "ws" + strings.TrimPrefix(server.URL, "http"), NoiseCertificateAuthority: root.Pub}
	client.SetPreLoginHTTPClient(&http.Client{Transport: transport})
	client.SetWebsocketHTTPClient(&http.Client{Transport: transport})
	lifetime, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); client.Disconnect(); transport.CloseIdleConnections() })
	if err := client.ConnectContext(lifetime); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finish:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native handshake did not complete")
	}
	return client, gate, cancel
}

func TestPinnedNativeSocketCancellationRequiresPhysicalRetirement(t *testing.T) {
	client, gate, cancelLifetime := nativeRetirementClient(t)
	raw, err := waBinary.Marshal(waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "synthetic-retirement", "to": "1@s.whatsapp.net"}, Content: []waBinary.Node{{Tag: "enc", Attrs: waBinary.Attrs{"type": "msg", "v": "2"}, Content: []byte{1, 2, 3}}}})
	if err != nil {
		t.Fatal(err)
	}
	gate.armed.Store(true)
	req, cancelRequest := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { _, err := client.SendPrebuiltStanza(req, raw, "", 30*time.Second); returned <- err }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native socket write not entered")
	}
	cancelRequest()
	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native request ignored cancellation")
	}
	select {
	case <-gate.closed:
		t.Fatal("per-request cancellation unexpectedly closed physical socket")
	default:
	}
	select {
	case <-gate.settled:
		t.Fatal("detached write falsely settled")
	default:
	}
	cancelLifetime()
	stopped := make(chan struct{})
	go func() { client.Disconnect(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("native Disconnect did not settle after lease cancellation")
	}
	select {
	case <-gate.closed:
	default:
		t.Fatal("Disconnect returned with old TCP socket open")
	}
	select {
	case <-gate.settled:
	case <-time.After(3 * time.Second):
		t.Fatal("detached writer did not settle")
	}
}
