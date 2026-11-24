package hap

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	stdhash "hash"
	"math/big"
	"strings"
	"testing"
)

const rfc5054Salt = "BEB25379D1A8581EB5A727673A2441EE"
const rfc5054A = "60975527035CF2AD1989806F0407210BC81EDC04E2762A56AFD529DDDA2D4393"
const rfc5054B = "E487CB59D31AC550471E81F00F6928E01DDA08E974A004F49E61F5D105284D20"

func hexBytes(s string) []byte {
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		panic(err)
	}
	return b
}

func digest(h func() stdhash.Hash, parts ...[]byte) []byte {
	sum := h()
	for _, p := range parts {
		sum.Write(p)
	}
	return sum.Sum(nil)
}

type accessorySRP struct {
	prime, generator, exponent, verifier, public *big.Int
}

func newAccessorySRP(primeHex string, generator int64, h func() stdhash.Hash, user, password string, salt []byte) accessorySRP {
	n := new(big.Int).SetBytes(hexBytes(primeHex))
	g := big.NewInt(generator)
	b := new(big.Int).SetBytes(hexBytes(rfc5054B))
	x := new(big.Int).SetBytes(digest(h, salt, digest(h, []byte(user+":"+password))))
	verifier := new(big.Int).Exp(g, x, n)
	k := new(big.Int).SetBytes(digest(h, n.Bytes(), g.FillBytes(make([]byte, len(n.Bytes())))))
	public := new(big.Int).Mod(new(big.Int).Add(new(big.Int).Exp(g, b, n), new(big.Int).Mul(k, verifier)), n)
	return accessorySRP{n, g, b, verifier, public}
}

func (s accessorySRP) premaster(h func() stdhash.Hash, public []byte) []byte {
	a := new(big.Int).SetBytes(public)
	width := len(s.prime.Bytes())
	u := new(big.Int).SetBytes(digest(h, a.FillBytes(make([]byte, width)), s.public.FillBytes(make([]byte, width))))
	base := new(big.Int).Mul(a, new(big.Int).Exp(s.verifier, u, s.prime))
	return new(big.Int).Exp(base, s.exponent, s.prime).Bytes()
}

func TestAirPlaySRP5054ReferenceAndHAP(t *testing.T) {
	// RFC 5054 附录 B 用 1024 位群和 SHA-1 校准测试端算法，HAP 使用附录 A.4 的群与 SHA-512。
	const rfcPrime = `
EEAF0AB9 ADB38DD6 9C33F80A FA8FC5E8 60726187 75FF3C0B 9EA2314C
9C256576 D674DF74 96EA81D3 383B4813 D692C6E0 E0D5D8E2 50B98BE4
8E495C1D 6089DAD1 5DC7D7B4 6154D6B6 CE8EF4AD 69B15D49 82559B29
7BCF1885 C529F566 660E57EC 68EDBC3C 05726CC0 2FD4CBF4 976EAA9A
FD5138FE 8376435B 9FC61D2F C0EB06E3`
	salt := hexBytes(rfc5054Salt)
	reference := newAccessorySRP(rfcPrime, 2, sha1.New, "alice", "password123", salt)
	public := new(big.Int).Exp(reference.generator, new(big.Int).SetBytes(hexBytes(rfc5054A)), reference.prime).Bytes()
	if !bytes.Equal(reference.public.Bytes(), hexBytes(`
BD0C6151 2C692C0C B6D041FA 01BB152D 4916A1E7 7AF46AE1 05393011
BAF38964 DC46A067 0DD125B9 5A981652 236F99D9 B681CBF8 7837EC99
6C6DA044 53728610 D0C6DDB5 8B318885 D7D82C7F 8DEB75CE 7BD4FBAA
37089E6F 9C6059F3 88838E7A 00030B33 1EB76840 910440B1 B27AAEAE
EB4012B7 D7665238 A8E3FB00 4B117B58`)) {
		t.Fatal("测试端公钥 B 与 RFC 5054 附录 B 不同")
	}
	if got := reference.premaster(sha1.New, public); !bytes.Equal(got, hexBytes(`
B0DC82BA BCF30674 AE450C02 87745E79 90A3381F 63B387AA F271A10D
233861E3 59B48220 F7C4693C 9AE12B0A 6F67809F 0876E2D0 13800D6C
41BB59B6 D5979B5C 00A172B4 A2A5903A 0BDCAF8A 709585EB 2AFAFA8F
3499B200 210DCC1F 10EB3394 3CD67FC8 8A2F39A4 BE5BEC4E C0A3212D
C346D7E4 74B29EDE 8A469FFE CA686E5A`)) {
		t.Fatal("测试端预主密钥与 RFC 5054 附录 B 不同")
	}
	for _, salt := range [][]byte{salt, salt[1:]} {
		accessory := newAccessorySRP(primeHex, 5, sha512.New, "Pair-Setup", "0123", salt)
		got, err := srp("0123", salt, accessory.public.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		key, proof := accessory.hapProof(salt, got.public)
		if !bytes.Equal(got.key, key) || !bytes.Equal(got.clientProof, proof) || !bytes.Equal(got.serverProof, digest(sha512.New, got.public, proof, key)) {
			t.Fatalf("%d 字节盐 CLIENT 与 ACCESSORY 的密钥或证明不一致", len(salt))
		}
	}
	for _, challenge := range []struct{ salt, public []byte }{
		{nil, []byte{1}}, {make([]byte, 17), []byte{1}}, {salt, []byte{0}}, {salt, hexBytes(primeHex)},
	} {
		if _, err := srp("0123", challenge.salt, challenge.public); err == nil {
			t.Error("接受了无效 SRP 挑战")
		}
	}
}

func (s accessorySRP) hapProof(salt, public []byte) ([]byte, []byte) {
	key := digest(sha512.New, s.premaster(sha512.New, public))
	groupHash := digest(sha512.New, s.prime.Bytes())
	for i, b := range digest(sha512.New, s.generator.Bytes()) {
		groupHash[i] ^= b
	}
	proof := digest(sha512.New, groupHash, digest(sha512.New, []byte("Pair-Setup")), salt, public, s.public.Bytes(), key)
	return key, proof
}
