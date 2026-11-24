package hap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const setupPath = "/pair-setup"

// PairTransient 在 M4 核对接收端的 SRP 证明，直接在同一连接上启用加密。
func PairTransient(ctx context.Context, client *rtsp.Client) (*Session, error) {
	p := pairing{client, "4"}
	m2, err := p.exchange(ctx, setupPath, 1, field{tagMethod, []byte{0}}, field{tagFlags, []byte{0x10}})
	if err != nil {
		return nil, err
	}
	session, err := p.prove(ctx, "3939", m2)
	if err != nil {
		return nil, err
	}
	return session.enable(client)
}

func (p pairing) prove(ctx context.Context, pin string, m2 message) (*Session, error) {
	result, err := srp(pin, m2[tagSalt], m2[tagPublicKey])
	if err != nil {
		return nil, err
	}
	m4, err := p.exchange(ctx, setupPath, 3, field{tagPublicKey, result.public}, field{tagProof, result.clientProof})
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(m4[tagProof], result.serverProof) != 1 {
		return nil, errors.New("HAP SRP 接收端证明无效")
	}
	return &Session{secret: result.key}, nil
}

// PINSetup 将同一条连接上的 SRP 挑战保留至用户输入 PIN。
type PINSetup struct {
	pairing   pairing
	mu        sync.Mutex
	challenge message
}

// BeginPIN 先请求接收端显示 PIN，再取得 SRP 挑战。
func BeginPIN(ctx context.Context, client *rtsp.Client) (*PINSetup, error) {
	p := pairing{client, "3"}
	if _, err := p.post(ctx, "/pair-pin-start", nil); err != nil {
		return nil, err
	}
	challenge, err := p.exchange(ctx, setupPath, 1, field{tagMethod, []byte{0}})
	if err != nil {
		return nil, err
	}
	return &PINSetup{pairing: p, challenge: challenge}, nil
}

// Finish 验证屏幕上的四位 PIN，并在 M6 验证接收端的长期身份后交付凭据。
func (p *PINSetup) Finish(ctx context.Context, pin string) (Credentials, error) {
	if len(pin) != 4 {
		return Credentials{}, errors.New("AirPlay 屏幕 PIN 须为四位数字")
	}
	for _, digit := range pin {
		if digit < '0' || digit > '9' {
			return Credentials{}, errors.New("AirPlay 屏幕 PIN 须为四位数字")
		}
	}
	p.mu.Lock()
	challenge := p.challenge
	p.challenge = nil
	p.mu.Unlock()
	if challenge == nil {
		return Credentials{}, errors.New("HAP 配对挑战已使用")
	}
	session, err := p.pairing.prove(ctx, pin, challenge)
	if err != nil {
		return Credentials{}, err
	}
	return session.exchangeIdentities(ctx, p.pairing)
}

func (s *Session) exchangeIdentities(ctx context.Context, p pairing) (Credentials, error) {
	cipher, err := s.pairingCipher("Pair-Setup-Encrypt")
	if err != nil {
		return Credentials{}, err
	}
	signingPrefix, err := s.pairingKey("Pair-Setup-Controller-Sign")
	if err != nil {
		return Credentials{}, err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Credentials{}, err
	}
	controllerID := uuid.New()
	signature := ed25519.Sign(private, joined(signingPrefix, []byte(controllerID), public))
	identity := encode(field{tagIdentifier, []byte(controllerID)}, field{tagPublicKey, public}, field{tagSignature, signature})
	m6, err := p.exchange(ctx, setupPath, 5, field{tagEncrypted, cipher.Seal(nil, nonce("PS-Msg05"), identity, nil)})
	if err != nil {
		return Credentials{}, err
	}
	accessory, err := openMessage(cipher, "PS-Msg06", m6[tagEncrypted])
	if err != nil {
		return Credentials{}, err
	}
	id, key := accessory[tagIdentifier], accessory[tagPublicKey]
	if !validIdentifier(id) || len(key) != ed25519.PublicKeySize {
		return Credentials{}, errors.New("HAP 接收端长期身份无效")
	}
	signingPrefix, err = s.pairingKey("Pair-Setup-Accessory-Sign")
	if err != nil {
		return Credentials{}, err
	}
	if !ed25519.Verify(key, joined(signingPrefix, id, key), accessory[tagSignature]) {
		return Credentials{}, errors.New("HAP 接收端长期身份签名无效")
	}
	return Credentials{ControllerID: controllerID, ControllerSeed: private.Seed(), AccessoryID: string(id), AccessoryPublicKey: key}, nil
}
