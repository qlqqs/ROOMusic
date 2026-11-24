package raop

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
)

func TestAirPlayClassicRSAWrappedAES(t *testing.T) {
	// 使用固定测试熵检验 OAEP 密钥包装，不需要真实接收端的私钥。
	random := bytes.Repeat([]byte{0x6b}, 128)
	original := rand.Reader
	rand.Reader = bytes.NewReader(random)
	defer func() { rand.Reader = original }()
	attrs, encrypt, err := keyExchange("2, 0, 1")
	if err != nil || encrypt == nil {
		t.Fatalf("RSA/AES 协商错误 %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(attrs, "\r\n"), "\r\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "a=rsaaeskey:") || !strings.HasPrefix(lines[1], "a=aesiv:") {
		t.Fatalf("SDP 加密属性结构错误")
	}
	wrapped, wrapErr := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(lines[0], "a=rsaaeskey:"))
	iv, ivErr := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(lines[1], "a=aesiv:"))
	if wrapErr != nil || ivErr != nil || len(wrapped) != 256 || !bytes.Equal(iv, random[:16]) {
		t.Fatalf("RSA 包装或 AES IV 格式错误，%v / %v", wrapErr, ivErr)
	}
	n, err := base64.StdEncoding.DecodeString(publicModulus)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := rsa.EncryptOAEP(sha1.New(), bytes.NewReader(random), &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: 65537}, random[:16], nil)
	if err != nil || !bytes.Equal(wrapped, expected) {
		t.Fatalf("SDP 未用预期 RSA OAEP SHA-1 包装 128 位 AES 密钥，错误 %v", err)
	}
	pcm := bytes.Repeat([]byte{0x31}, 35)
	first := encrypt(nil, bytes.Clone(pcm), 1)
	second := encrypt(nil, bytes.Clone(pcm), 2)
	if !bytes.Equal(first, second) || bytes.Equal(pcm[:32], first[:32]) || !bytes.Equal(pcm[32:], first[32:]) {
		t.Fatal("RAOP AES 未逐包重置 IV，或错误加密了尾部")
	}
	block, err := aes.NewCipher(random[:16])
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(first[:32], first[:32])
	if !bytes.Equal(first, pcm) {
		t.Fatal("使用协商的 AES 密钥与 IV 无法还原载荷")
	}
}

func TestAirPlayClassicEncryptionMethods(t *testing.T) {
	for _, methods := range []string{"", "3, 0"} {
		if attrs, encrypt, err := keyExchange(methods); attrs != "" || encrypt != nil || err != nil {
			t.Errorf("RAOP 加密选项 %q 未接受明文，错误 %v", methods, err)
		}
	}
	if _, _, err := keyExchange("3,4,5"); err == nil {
		t.Error("接受了无已知实现的加密选项")
	}
}
