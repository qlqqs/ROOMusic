// Package secret 管理管家的实例密钥与按用户、用途绑定的认证加密。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
)

const ciphertextVersion byte = 1

// ErrCannotOpen 表示密文无法解密，调用方应将对应密钥标为需要重新填写。
var ErrCannotOpen = errors.New("无法解密密钥，请重新填写")

// Store 持有实例密钥文件路径，首次使用时加载密钥，之后在内存中复用。
// 一个实例由调用方共享一个 Store；Seal 和 Open 可并发调用。
type Store struct {
	path string
	mu   sync.Mutex
	aead cipher.AEAD
}

// New 创建密钥存储；path 由调用方从实例数据目录拼出，构造时不读写文件。
func New(path string) *Store { return &Store{path: path} }

// Seal 用随机 nonce 加密明文，并绑定用户 ID 与用途。
// 返回格式依次是一个版本字节、nonce、密文与认证标签。
func (store *Store) Seal(userID, purpose string, plaintext []byte) ([]byte, error) {
	aead, err := store.load(true)
	if err != nil {
		return nil, err
	}
	output := make([]byte, 1+aead.NonceSize())
	output[0] = ciphertextVersion
	if _, err := rand.Read(output[1:]); err != nil {
		return nil, errors.New("生成加密随机数失败")
	}
	return aead.Seal(output, output[1:], plaintext, associatedData(userID, purpose)), nil
}

// Open 解密同一用户、同一用途的密文；任何失败均返回 ErrCannotOpen。
func (store *Store) Open(userID, purpose string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 || ciphertext[0] != ciphertextVersion {
		return nil, ErrCannotOpen
	}
	aead, err := store.load(false)
	if err != nil || len(ciphertext) < 1+aead.NonceSize()+aead.Overhead() {
		return nil, ErrCannotOpen
	}
	end := 1 + aead.NonceSize()
	plaintext, err := aead.Open(nil, ciphertext[1:end], ciphertext[end:], associatedData(userID, purpose))
	if err != nil {
		return nil, ErrCannotOpen
	}
	return plaintext, nil
}

// LastFour 返回明文最后四个 Unicode 字符；不足四个时返回全部。
func LastFour(plaintext string) string {
	characters := []rune(plaintext)
	if len(characters) <= 4 {
		return plaintext
	}
	return string(characters[len(characters)-4:])
}

func (store *Store) load(create bool) (cipher.AEAD, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.aead != nil {
		return store.aead, nil
	}
	key, err := loadKey(store.path, create)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("初始化实例密钥失败")
	}
	store.aead, err = cipher.NewGCM(block)
	return store.aead, err
}

func associatedData(userID, purpose string) []byte {
	data := binary.BigEndian.AppendUint64(nil, uint64(len(userID)))
	data = append(data, userID...)
	data = binary.BigEndian.AppendUint64(data, uint64(len(purpose)))
	return append(data, purpose...)
}

// SearchKeyPurpose 将搜索密钥与模型密钥绑定到不同用途，仍使用同一加密实现。
const SearchKeyPurpose = "steward.search"
