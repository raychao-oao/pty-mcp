package mcp

import (
	"context"
	"crypto/ecdh"
	"crypto/hpke"
	"encoding/json"
	"testing"
	"time"

	"github.com/raychao-oao/cred-proto/pkg/credproto"
	"github.com/raychao-oao/pty-mcp/internal/buffer"
	"github.com/raychao-oao/pty-mcp/internal/session"
)

// fakeSession is a minimal session.Session for testing InjectSecret.
type fakeSession struct {
	id      string
	written []string
	rb      *buffer.RingBuffer
}

func (f *fakeSession) ID() string                            { return f.id }
func (f *fakeSession) Type() string                          { return "local" }
func (f *fakeSession) Write(input string) error              { f.written = append(f.written, input); return nil }
func (f *fakeSession) WriteRaw(data string) error            { f.written = append(f.written, data); return nil }
func (f *fakeSession) ReadScreen(timeoutMs int) (string, bool) { return "", true }
func (f *fakeSession) IsAlive() bool                         { return true }
func (f *fakeSession) Close() error                          { return nil }
func (f *fakeSession) Buffer() *buffer.RingBuffer            { return f.rb }
func (f *fakeSession) PollRemote(ctx context.Context)        {}
func (f *fakeSession) Resize(rows, cols int) error           { return nil }

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	mgr := session.NewManager(0)
	h := NewHandler(mgr, nil)
	h.identityKeyPath = t.TempDir() + "/identity.key"
	return h
}

// sealBoxForTest creates a SealedBox from a bundle's session pubkey — mirrors what cred-mcp/internal/seal does.
func sealBoxForTest(t *testing.T, bundle *credproto.ConsumerBundle, itemID, purpose, boxID string, boxExp time.Time, plaintext []byte) json.RawMessage {
	t.Helper()
	info := credproto.MarshalInfo(bundle.ConsumerID, bundle.SessionID, itemID, purpose, bundle.ExpiresAt, boxExp, boxID)
	ecdhPub, err := ecdh.X25519().NewPublicKey(bundle.SessionPubKey)
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}
	pub, err := hpke.NewDHKEMPublicKey(ecdhPub)
	if err != nil {
		t.Fatalf("NewDHKEMPublicKey: %v", err)
	}
	encappedKey, sender, err := hpke.NewSender(pub, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), info)
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	ct, err := sender.Seal(nil, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	box := credproto.SealedBox{
		BoxID: boxID, ConsumerID: bundle.ConsumerID, SessionID: bundle.SessionID,
		ItemID: itemID, Purpose: purpose, EncappedKey: encappedKey, Ciphertext: ct, ExpiresAt: boxExp,
	}
	b, err := json.Marshal(box)
	if err != nil {
		t.Fatalf("Marshal SealedBox: %v", err)
	}
	return b
}

func getBundleFromResult(t *testing.T, result any) *credproto.ConsumerBundle {
	t.Helper()
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	bundle, ok := m["bundle"].(*credproto.ConsumerBundle)
	if !ok {
		t.Fatalf("expected *credproto.ConsumerBundle, got %T", m["bundle"])
	}
	return bundle
}

func TestGetCredentialBundle_ReturnsValidBundle(t *testing.T) {
	h := newTestHandler(t)
	params, _ := json.Marshal(map[string]any{"consumer_id": "pty-mcp", "ttl_seconds": 60})

	result, err := h.GetCredentialBundle(params)
	if err != nil {
		t.Fatalf("GetCredentialBundle: %v", err)
	}
	bundle := getBundleFromResult(t, result)
	if err := credproto.VerifyBundle(bundle); err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	m := result.(map[string]any)
	if m["session_key_id"] != bundle.SessionID {
		t.Fatalf("session_key_id %q != bundle.SessionID %q", m["session_key_id"], bundle.SessionID)
	}
}

func TestGetCredentialBundle_DefaultConsumerID(t *testing.T) {
	h := newTestHandler(t)
	params, _ := json.Marshal(map[string]any{})

	result, err := h.GetCredentialBundle(params)
	if err != nil {
		t.Fatalf("GetCredentialBundle: %v", err)
	}
	bundle := getBundleFromResult(t, result)
	if bundle.ConsumerID != "pty-mcp" {
		t.Fatalf("expected consumer_id 'pty-mcp', got %q", bundle.ConsumerID)
	}
}

func TestGetCredentialBundle_StoresSessionKey(t *testing.T) {
	h := newTestHandler(t)
	params, _ := json.Marshal(map[string]any{"ttl_seconds": 60})

	result, _ := h.GetCredentialBundle(params)
	bundle := getBundleFromResult(t, result)

	h.credStore.mu.Lock()
	_, ok := h.credStore.creds[bundle.SessionID]
	h.credStore.mu.Unlock()
	if !ok {
		t.Fatal("session key not found in credStore after GetCredentialBundle")
	}
}

func TestGetCredentialBundle_UniqueSessionIDs(t *testing.T) {
	h := newTestHandler(t)
	params, _ := json.Marshal(map[string]any{})

	r1, _ := h.GetCredentialBundle(params)
	r2, _ := h.GetCredentialBundle(params)
	b1 := getBundleFromResult(t, r1)
	b2 := getBundleFromResult(t, r2)
	if b1.SessionID == b2.SessionID {
		t.Fatal("two bundles from same handler must have unique session IDs")
	}
}

func TestInjectSecret_RoundTrip(t *testing.T) {
	h := newTestHandler(t)

	// Get bundle
	params, _ := json.Marshal(map[string]any{"ttl_seconds": 60})
	result, err := h.GetCredentialBundle(params)
	if err != nil {
		t.Fatalf("GetCredentialBundle: %v", err)
	}
	bundle := getBundleFromResult(t, result)

	// Seal a secret using the bundle's session pubkey
	want := []byte("s3cr3t-p4ssw0rd")
	boxExp := time.Now().Add(5 * time.Minute).UTC()
	sealedBoxJSON := sealBoxForTest(t, bundle, "item-1", "ssh-login", "box-1", boxExp, want)

	// Register a fake PTY session
	fs := &fakeSession{id: "sess-1", rb: buffer.NewRingBuffer(1024)}
	if err := h.mgr.Add(fs, "test-target"); err != nil {
		t.Fatalf("Add session: %v", err)
	}

	// Inject
	injectParams, _ := json.Marshal(map[string]any{
		"pty_session_id": "sess-1",
		"sealed_box":     json.RawMessage(sealedBoxJSON),
	})
	injectResult, err := h.InjectSecret(injectParams)
	if err != nil {
		t.Fatalf("InjectSecret: %v", err)
	}

	// Result must only contain success:true — no plaintext
	resultJSON, _ := json.Marshal(injectResult)
	if string(resultJSON) != `{"success":true}` {
		t.Fatalf("result must not contain plaintext, got: %s", resultJSON)
	}

	// Plaintext + "\r" must have been written to the session
	if len(fs.written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(fs.written))
	}
	if fs.written[0] != string(want)+"\r" {
		t.Fatalf("got written %q, want %q", fs.written[0], string(want)+"\r")
	}
}

func TestInjectSecret_SingleUse(t *testing.T) {
	h := newTestHandler(t)

	params, _ := json.Marshal(map[string]any{"ttl_seconds": 60})
	result, _ := h.GetCredentialBundle(params)
	bundle := getBundleFromResult(t, result)

	boxExp := time.Now().Add(5 * time.Minute).UTC()
	sealedBoxJSON := sealBoxForTest(t, bundle, "item-1", "ssh-login", "box-1", boxExp, []byte("s3cr3t"))

	fs := &fakeSession{id: "sess-1", rb: buffer.NewRingBuffer(1024)}
	h.mgr.Add(fs, "test-target") //nolint:errcheck

	injectParams, _ := json.Marshal(map[string]any{
		"pty_session_id": "sess-1",
		"sealed_box":     json.RawMessage(sealedBoxJSON),
	})

	if _, err := h.InjectSecret(injectParams); err != nil {
		t.Fatalf("first InjectSecret: %v", err)
	}
	if _, err := h.InjectSecret(injectParams); err == nil {
		t.Fatal("expected error on second InjectSecret (session key already consumed)")
	}
}

func TestInjectSecret_UnknownSessionKey(t *testing.T) {
	h := newTestHandler(t)

	box := credproto.SealedBox{
		BoxID: "x", ConsumerID: "pty-mcp", SessionID: "nonexistent",
		ItemID: "y", Purpose: "z", ExpiresAt: time.Now().Add(time.Minute),
	}
	boxJSON, _ := json.Marshal(box)

	fs := &fakeSession{id: "sess-1", rb: buffer.NewRingBuffer(1024)}
	h.mgr.Add(fs, "test-target") //nolint:errcheck

	params, _ := json.Marshal(map[string]any{
		"pty_session_id": "sess-1",
		"sealed_box":     json.RawMessage(boxJSON),
	})
	_, err := h.InjectSecret(params)
	if err == nil {
		t.Fatal("expected error for unknown session_id")
	}
}
