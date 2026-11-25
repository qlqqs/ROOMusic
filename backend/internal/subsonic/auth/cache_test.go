package auth

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestSubsonicPasswordCacheExpiryAndBinding(t *testing.T) {
	const id, password, next = "fixture-user", "subsonic-cache-password", "subsonic-next-cache-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var cache passwordCache
	started := time.Now()
	if !cache.verify(id, string(hash), password) {
		t.Fatal("正确密码首次验证失败")
	}
	entry := cache.entries[id]
	if ttl := entry.until.Sub(started); ttl < 5*time.Minute || entry.until.After(time.Now().Add(5*time.Minute)) {
		t.Fatalf("成功验证的缓存有效期应为 5 分钟，实际为 %v", ttl)
	}
	if !cache.verify(id, string(hash), password) || cache.entries[id] != entry {
		t.Fatal("再次验证同一凭据时应复用原缓存")
	}
	if cache.verify(id, string(hash), next) {
		t.Error("缓存期内错误的候选密码仍应被拒绝")
	}
	if cache.verify(id, string(newHash), password) {
		t.Error("缓存期内变更密码哈希应立即拒绝旧密码")
	}

	entry.until = time.Now().Add(-time.Second)
	cache.entries[id] = entry
	if !cache.verify(id, string(hash), password) || !cache.entries[id].until.After(time.Now()) {
		t.Fatal("缓存到期后应重新校验并更新有效期")
	}
}
