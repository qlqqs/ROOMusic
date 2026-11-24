package hap

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
)

// PairVerify 用双方长期签名认证本次 X25519 交换，验证 M4 后切换控制通道。
func PairVerify(ctx context.Context, client *rtsp.Client, creds Credentials) (*Session, error) {
	if len(creds.ControllerSeed) != ed25519.SeedSize || len(creds.AccessoryPublicKey) != ed25519.PublicKeySize ||
		!validIdentifier([]byte(creds.ControllerID)) || !validIdentifier([]byte(creds.AccessoryID)) {
		return nil, errors.New("HAP 长期凭据无效")
	}
	localKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	local := localKey.PublicKey().Bytes()
	const path = "/pair-verify"
	p := pairing{client, "3"}
	m2, err := p.exchange(ctx, path, 1, field{tagPublicKey, local})
	if err != nil {
		return nil, err
	}
	remote := m2[tagPublicKey]
	remoteKey, err := ecdh.X25519().NewPublicKey(remote)
	if err != nil {
		return nil, err
	}
	shared, err := localKey.ECDH(remoteKey)
	if err != nil {
		return nil, err
	}
	session := &Session{secret: shared}
	cipher, err := session.pairingCipher("Pair-Verify-Encrypt")
	if err != nil {
		return nil, err
	}
	identity, err := openMessage(cipher, "PV-Msg02", m2[tagEncrypted])
	if err != nil {
		return nil, err
	}
	id := identity[tagIdentifier]
	if !bytes.Equal(id, []byte(creds.AccessoryID)) ||
		!ed25519.Verify(creds.AccessoryPublicKey, joined(remote, id, local), identity[tagSignature]) {
		return nil, errors.New("HAP 接收端身份验证失败")
	}
	controllerID := []byte(creds.ControllerID)
	private := ed25519.NewKeyFromSeed(creds.ControllerSeed)
	proof := ed25519.Sign(private, joined(local, controllerID, remote))
	payload := encode(field{tagIdentifier, controllerID}, field{tagSignature, proof})
	_, err = p.exchange(ctx, path, 3, field{tagEncrypted, cipher.Seal(nil, nonce("PV-Msg03"), payload, nil)})
	if err != nil {
		return nil, err
	}
	return session.enable(client)
}
