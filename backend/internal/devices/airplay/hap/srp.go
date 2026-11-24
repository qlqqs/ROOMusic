package hap

import (
	"bytes"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"

	"filippo.io/bigmod"
)

// RFC 5054 附录 A.4 的 3072 位群使用生成元 5。
// HAP 采用 SHA-512，证明中的整数取最短大端编码；k、u 的操作数补齐至群宽。
// 行为参考 airplay2-sender-cpp（Apache-2.0）的 SRP 整数编码。
// https://github.com/akustikrausch/airplay2-sender-cpp/blob/main/src/airplay_crypto.cpp
const primeHex = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E08" +
	"8A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B" +
	"302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9" +
	"A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE6" +
	"49286651ECE45B3DC2007CB8A163BF0598DA48361C55D39A69163FA8" +
	"FD24CF5F83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
	"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3BE39E772C" +
	"180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718" +
	"3995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D" +
	"04507A33A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7D" +
	"B3970F85A6E1E4C7ABF5AE8CDB0933D71E8C94E04A25619DCEE3D226" +
	"1AD2EE6BF12FFA06D98A0864D87602733EC86A64521F2B18177B200C" +
	"BBE117577A615D6C770988C0BAD946E208E24FA074E5AB3143DB5BFC" +
	"E0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"

var modulus = func() *bigmod.Modulus {
	prime, err := hex.DecodeString(primeHex)
	if err != nil {
		panic(err)
	}
	group, err := bigmod.NewModulus(prime)
	if err != nil {
		panic(err)
	}
	return group
}()

func hash(parts ...[]byte) []byte {
	h := sha512.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// small 仅接收本地产生的 512 位以内的数，均小于模数。
func small(b []byte) *bigmod.Nat {
	value, err := bigmod.NewNat().SetBytes(b, modulus)
	if err != nil {
		panic(err)
	}
	return value
}

type srpResult struct {
	public, clientProof, serverProof, key []byte
}

func srp(pin string, salt, receiverPublic []byte) (srpResult, error) {
	b, err := bigmod.NewNat().SetBytes(receiverPublic, modulus)
	// 行为参考 shairport-sync 5.6.3：128 位随机盐可能使用无前导零的最短整数编码。
	if len(salt) == 0 || len(salt) > 16 {
		return srpResult{}, errors.New("HAP SRP 盐长度无效")
	}
	if err != nil || b.IsZero() == 1 {
		return srpResult{}, errors.New("HAP SRP 挑战无效")
	}
	g := small([]byte{5})
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return srpResult{}, err
	}
	a := bigmod.NewNat().Exp(g, secret, modulus).Bytes(modulus)
	prime := modulus.Nat().Bytes(modulus)
	k := small(hash(prime, g.Bytes(modulus)))
	u := small(hash(a, b.Bytes(modulus)))
	if u.IsZero() == 1 {
		return srpResult{}, errors.New("HAP SRP 扰动参数无效")
	}
	x := hash(salt, hash([]byte("Pair-Setup:"), []byte(pin)))
	verifier := bigmod.NewNat().Exp(g, x, modulus)
	base := b.Sub(k.Mul(verifier, modulus), modulus)
	if base.IsZero() == 1 {
		return srpResult{}, errors.New("HAP SRP 共享参数无效")
	}
	exponent := u.Mul(small(x), modulus).Add(small(secret), modulus).Bytes(modulus)
	// a + u*x 最多 1025 位，使用固定 129 字节计算秘密幂。
	shared := bigmod.NewNat().Exp(base, exponent[len(exponent)-129:], modulus).Bytes(modulus)
	key := hash(bytes.TrimLeft(shared, "\x00"))
	groupHash := hash(prime)
	subtle.XORBytes(groupHash, groupHash, hash([]byte{5}))
	public := bytes.TrimLeft(a, "\x00")
	proof := hash(groupHash, hash([]byte("Pair-Setup")), salt, public, bytes.TrimLeft(receiverPublic, "\x00"), key)
	return srpResult{
		public: public, clientProof: proof, serverProof: hash(public, proof, key), key: key,
	}, nil
}
