package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/raychao-oao/cred-proto/pkg/consumersdk"
	"github.com/raychao-oao/cred-proto/pkg/credproto"
)

// pendingCred holds a session keypair alongside the bundle it was created with.
// Both are needed to decrypt a SealedBox: sessionKey for HPKE, bundle for info binding.
// Consumed on first use.
type pendingCred struct {
	sessionKey *consumersdk.SessionKey
	bundle     *credproto.ConsumerBundle
	expiresAt  time.Time
}

type credentialStore struct {
	mu    sync.Mutex
	creds map[string]*pendingCred // keyed by bundle.SessionID
}

func newCredentialStore() *credentialStore {
	return &credentialStore{creds: make(map[string]*pendingCred)}
}

func (cs *credentialStore) store(bundle *credproto.ConsumerBundle, sk *consumersdk.SessionKey) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.purgeExpiredLocked()
	cs.creds[bundle.SessionID] = &pendingCred{
		sessionKey: sk,
		bundle:     bundle,
		expiresAt:  bundle.ExpiresAt,
	}
}

// take removes and returns the credential for sessionID, or (nil, false) if not found.
func (cs *credentialStore) take(sessionID string) (*pendingCred, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	p, ok := cs.creds[sessionID]
	if !ok {
		return nil, false
	}
	delete(cs.creds, sessionID)
	return p, true
}

func (cs *credentialStore) purgeExpiredLocked() {
	now := time.Now()
	for id, p := range cs.creds {
		if now.After(p.expiresAt) {
			delete(cs.creds, id)
		}
	}
}

func defaultIdentityKeyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "pty-mcp", "identity.key")
}

// GetCredentialBundleParams are the parameters for the get_credential_bundle tool.
type GetCredentialBundleParams struct {
	ConsumerID string `json:"consumer_id"` // default: "pty-mcp"
	TTLSeconds int    `json:"ttl_seconds"` // default: 300, max: 3600
}

// GetCredentialBundle generates a signed ConsumerBundle for HPKE-sealed credential delivery.
// The bundle's public keys are safe to pass to cred-mcp; the session private key stays in memory.
func (h *Handler) GetCredentialBundle(params json.RawMessage) (any, error) {
	var p GetCredentialBundleParams
	if err := UnmarshalMcpArgs(params, &p); err != nil {
		return nil, err
	}
	if p.ConsumerID == "" {
		p.ConsumerID = "pty-mcp"
	}
	if p.TTLSeconds <= 0 {
		p.TTLSeconds = 300
	}
	if p.TTLSeconds > 3600 {
		p.TTLSeconds = 3600
	}

	ik, err := consumersdk.LoadOrGenerateIdentityKey(h.identityKeyPath)
	if err != nil {
		return nil, fmt.Errorf("identity key: %w", err)
	}
	sk, err := consumersdk.GenerateSessionKey()
	if err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	bundle, err := consumersdk.NewBundle(ik, sk, p.ConsumerID, time.Duration(p.TTLSeconds)*time.Second)
	if err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}

	h.credStore.store(bundle, sk)

	return map[string]any{
		"bundle":         bundle,
		"session_key_id": bundle.SessionID,
		"expires_at":     bundle.ExpiresAt,
	}, nil
}

// InjectSecret decrypts a SealedBox from cred-mcp and writes the plaintext directly into a PTY
// session. The plaintext never appears in the tool result — only {"success":true} is returned.
func (h *Handler) InjectSecret(params json.RawMessage) (any, error) {
	var raw struct {
		PtySessionID string          `json:"pty_session_id"`
		SealedBox    json.RawMessage `json:"sealed_box"`
	}
	if err := json.Unmarshal(params, &raw); err != nil {
		return nil, err
	}

	var box credproto.SealedBox
	if err := json.Unmarshal(raw.SealedBox, &box); err != nil {
		return nil, fmt.Errorf("sealed_box: %w", err)
	}

	cred, ok := h.credStore.take(box.SessionID)
	if !ok {
		return nil, fmt.Errorf("no credential found for session_id %q (already used or expired)", box.SessionID)
	}

	plaintext, err := consumersdk.Open(&box, cred.sessionKey, cred.bundle)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()

	s, err := h.mgr.Get(raw.PtySessionID)
	if err != nil {
		return nil, err
	}
	if err := s.WriteRaw(string(plaintext) + "\r"); err != nil {
		return nil, fmt.Errorf("write to session: %w", err)
	}

	return map[string]any{"success": true}, nil
}
