// Package peer is the music side's half of the peer protocol every side of
// mediaserver speaks with its apps (the Aniko and KumoBox apps' "share"
// protocol, anime/internal/share): the server's X25519 key pair, whose
// fingerprint is its ID; requests a device signs with a secret only it and
// the server can make, checked here; answers signed back; settings sealed so
// only the device they're for can read them. Byte for byte as the anime
// side's identity.go, which Utako's own copy is checked against too (the
// vectors in testdata come from it).
package peer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Identity is the server's key pair.
type Identity struct {
	ID   string
	priv *ecdh.PrivateKey

	// The nonces of the requests checked lately (see Verify).
	mu     sync.Mutex
	nonces map[string]time.Time
	pruned time.Time
}

// Load returns the identity kept in file, made (and saved, readable by this
// user only) the first time.
func Load(file string) (*Identity, error) {
	var saved struct {
		Private string `json:"private"`
	}
	if raw, err := os.ReadFile(file); err == nil && json.Unmarshal(raw, &saved) == nil {
		if key, err := base64.StdEncoding.DecodeString(saved.Private); err == nil {
			if priv, err := ecdh.X25519().NewPrivateKey(key); err == nil {
				return New(priv), nil
			}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	saved.Private = base64.StdEncoding.EncodeToString(priv.Bytes())
	raw, _ := json.Marshal(saved)
	if err := WriteFile(file, raw, 0o600); err != nil {
		return nil, err
	}
	return New(priv), nil
}

// New is the identity of that private key.
func New(priv *ecdh.PrivateKey) *Identity {
	return &Identity{ID: Fingerprint(priv.PublicKey().Bytes()), priv: priv}
}

// FromPrivate is the identity of a private key in base64 (tests).
func FromPrivate(b64 string) (*Identity, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	return New(priv), nil
}

// PublicKey is the public key, as sent to the devices.
func (id *Identity) PublicKey() string {
	return base64.StdEncoding.EncodeToString(id.priv.PublicKey().Bytes())
}

// Fingerprint is the ID of the device with this public key: 16 letters and
// digits.
func Fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10]))
}

// ParsePublicKey checks a public key received with the ID it should have.
func ParsePublicKey(id, b64 string) ([]byte, error) {
	pub, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(pub) != 32 {
		return nil, errors.New("bad public key")
	}
	if Fingerprint(pub) != id {
		return nil, errors.New("the public key doesn't match the ID")
	}
	return pub, nil
}

// secret is what the server and the device with that public key have in
// common, for a purpose: only those two can make it.
func (id *Identity) secret(peerPub []byte, purpose string) ([]byte, error) {
	pk, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, err
	}
	shared, err := id.priv.ECDH(pk)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, shared)
	mac.Write([]byte(purpose))
	return mac.Sum(nil), nil
}

// SealFor encrypts something for the device with that public key (the
// settings it asks for): only it can read it, though it crosses the network
// as is. guest is the device, host the server.
func (id *Identity) SealFor(devicePub []byte, guest, host string, plain []byte) (string, error) {
	aead, err := id.cipher(devicePub, guest, host)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil)), nil
}

// OpenFrom decrypts what the device with that public key sealed: for the
// server (guest = the server, host = the device), as Utako sends its
// settings.
func (id *Identity) OpenFrom(devicePub []byte, guest, host, sealed string) ([]byte, error) {
	aead, err := id.cipher(devicePub, guest, host)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, errors.New("bad sealed data")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
}

func (id *Identity) cipher(peerPub []byte, guest, host string) (cipher.AEAD, error) {
	key, err := id.secret(peerPub, fmt.Sprintf("kumo-share-v1|seal|guest=%s|host=%s", guest, host))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// The headers a device's requests carry (their names from before Aniko's
// rename, as every side's).
const (
	HeaderID    = "X-Kumo-Peer-Id"
	HeaderKey   = "X-Kumo-Peer-Key"
	HeaderName  = "X-Kumo-Peer-Name"
	HeaderPort  = "X-Kumo-Peer-Port"
	HeaderToken = "X-Kumo-Peer-Token"
	HeaderUser  = "X-Kumo-Peer-User"
	HeaderTime  = "X-Kumo-Peer-Time"
	HeaderNonce = "X-Kumo-Peer-Nonce"
	// HeaderReply is in the server's answer: it proves the answer comes
	// from the server (made with the request's nonce, and its Date).
	HeaderReply = "X-Kumo-Peer-Reply"
)

const (
	// How far a request's time may be from the server's clock.
	clockSkew = 5 * time.Minute
	// How long a player may stream a song with the headers it was given
	// (mpv repeats the same request when it seeks: a ticket).
	ticketFor = 12 * time.Hour
	// MaxBody is the largest request body read to check it.
	MaxBody = 16 << 20
)

var (
	ErrOld    = errors.New("that app is too old for this server: update it")
	ErrClock  = errors.New("the clocks of the server and the device are too far apart")
	ErrReplay = errors.New("a request sent again")
)

// IsPeerRequest reports a signed request (which Verify checks).
func IsPeerRequest(r *http.Request) bool { return r.Header.Get(HeaderID) != "" }

// mac proves a request, or the answer to it, between a device (guest) and
// the server (host): only those two can make it, and it changes with each of
// its parts.
func (id *Identity) mac(peerPub []byte, guest, host string, parts ...string) (string, error) {
	key, err := id.secret(peerPub, fmt.Sprintf("kumo-share-v2|guest=%s|host=%s", guest, host))
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		fmt.Fprintf(m, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(m.Sum(nil)), nil
}

func requestParts(r *http.Request, body string) []string {
	h := r.Header
	return []string{"request", r.Method, r.URL.RequestURI(), h.Get(HeaderTime), h.Get(HeaderNonce), h.Get(HeaderName), h.Get(HeaderPort), h.Get(HeaderUser), body}
}

// Signature is a request's, once its token is checked: what proves the
// answer.
type Signature struct {
	id          *Identity
	peerPub     []byte
	guest, host string
	nonce       string
}

// Reply is what proves the answer to the request comes from the server.
func (s *Signature) Reply(date string) (string, error) {
	return s.id.mac(s.peerPub, s.guest, s.host, "reply", s.nonce, date)
}

// Caller is who sent a request, once checked.
type Caller struct {
	ID, Name string
	Pub      []byte
	User     int
	// Ticket: a song's request a player repeats (see IsTicket), maybe for
	// hours.
	Ticket bool
}

// IsTicket reports a request a player may repeat: a GET of a song.
func IsTicket(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/peer/local/file"
}

// Verify checks that a request comes from the device it says, as it was
// made, now and for the first time; its body (MaxBody at most) stays to be
// read. The signature (to prove the answer) is there once the token is
// right, also when the request is refused for its time.
func (id *Identity) Verify(r *http.Request, now time.Time) (*Caller, *Signature, error) {
	c := &Caller{ID: r.Header.Get(HeaderID)}
	pub, err := ParsePublicKey(c.ID, r.Header.Get(HeaderKey))
	if err != nil {
		return nil, nil, err
	}
	if r.Header.Get(HeaderTime) == "" {
		return nil, nil, ErrOld
	}
	body, err := receivedBody(r)
	if err != nil {
		return nil, nil, err
	}
	want, err := id.mac(pub, c.ID, id.ID, requestParts(r, body)...)
	if err != nil {
		return nil, nil, err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(r.Header.Get(HeaderToken))) != 1 {
		return nil, nil, errors.New("wrong token")
	}
	nonce := r.Header.Get(HeaderNonce)
	sig := &Signature{id: id, peerPub: pub, guest: c.ID, host: id.ID, nonce: nonce}
	sec, err := strconv.ParseInt(r.Header.Get(HeaderTime), 10, 64)
	if err != nil {
		return nil, sig, errors.New("bad time")
	}
	at := time.Unix(sec, 0)
	switch {
	case nonce == "" && IsTicket(r):
		if at.Before(now.Add(-ticketFor)) || at.After(now.Add(clockSkew)) {
			return nil, sig, ErrClock
		}
	case nonce == "" || len(nonce) > 64:
		return nil, sig, errors.New("no nonce")
	case at.Before(now.Add(-clockSkew)) || at.After(now.Add(clockSkew)):
		return nil, sig, ErrClock
	case !id.firstUse(nonce, at.Add(clockSkew), now):
		return nil, sig, ErrReplay
	}
	c.Pub = pub
	c.Ticket = nonce == ""
	c.Name, _ = url.QueryUnescape(r.Header.Get(HeaderName))
	c.Name = CleanName(c.Name)
	c.User, _ = strconv.Atoi(r.Header.Get(HeaderUser))
	return c, sig, nil
}

// receivedBody is the SHA-256 of the body of a request received, which
// stays to be read.
func receivedBody(r *http.Request) (string, error) {
	var b []byte
	if r.Body != nil && r.Body != http.NoBody {
		var err error
		b, err = io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
		r.Body.Close()
		if err != nil {
			return "", err
		}
		if len(b) > MaxBody {
			return "", errors.New("request too large")
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// firstUse notes a request's nonce until its time is over (until): it's the
// first time it's seen.
func (id *Identity) firstUse(nonce string, until, now time.Time) bool {
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.nonces == nil {
		id.nonces = map[string]time.Time{}
	}
	if len(id.nonces) > 4096 && now.Sub(id.pruned) > 10*time.Second {
		for n, t := range id.nonces {
			if now.After(t) {
				delete(id.nonces, n)
			}
		}
		id.pruned = now
	}
	if _, seen := id.nonces[nonce]; seen {
		return false
	}
	id.nonces[nonce] = until
	return true
}

// Sign signs a request to the server (hostID, hostPub) as a device does:
// what Utako's src/hub/identity.rs does, for tests. It returns the request's
// nonce ("" for a ticket).
func (id *Identity) Sign(req *http.Request, body []byte, hostID string, hostPub []byte, name string, at time.Time, ticket bool) (string, error) {
	nonce := ""
	if !ticket {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		nonce = hex.EncodeToString(b)
	}
	sum := sha256.Sum256(body)
	h := req.Header
	h.Set(HeaderID, id.ID)
	h.Set(HeaderKey, id.PublicKey())
	h.Set(HeaderName, url.QueryEscape(name))
	h.Set(HeaderPort, "0")
	h.Del(HeaderUser)
	h.Set(HeaderTime, strconv.FormatInt(at.Unix(), 10))
	h.Set(HeaderNonce, nonce)
	tok, err := id.mac(hostPub, id.ID, hostID, requestParts(req, hex.EncodeToString(sum[:]))...)
	if err != nil {
		return "", err
	}
	h.Set(HeaderToken, tok)
	return nonce, nil
}

// CheckReply checks, as a device does, that an answer was signed by the
// server (tests).
func (id *Identity) CheckReply(hostID string, hostPub []byte, nonce string, resp *http.Response) error {
	want, err := id.mac(hostPub, id.ID, hostID, "reply", nonce, resp.Header.Get("Date"))
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(want), []byte(resp.Header.Get(HeaderReply))) {
		return errors.New("the answer isn't signed by the server")
	}
	return nil
}

// CleanName keeps a device's name printable and short.
func CleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}

// WriteFile writes a file whole or not at all (through a file next to it).
func WriteFile(name string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}
