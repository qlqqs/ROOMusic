package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabasePasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	password := " space:@/?#%&'\\末尾 "
	if err := os.WriteFile(path, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROOMUSIC_DATABASE_URL", "postgres://roomusic@postgres:5432/roomusic?sslmode=disable")
	t.Setenv("ROOMUSIC_DB_PASSWORD_FILE", path)
	got, err := DatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := parsed.User.Password()
	if actual != password || parsed.Host != "postgres:5432" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatal("密码或连接参数未保留")
	}
	settings, err := LoadBackup()
	if err != nil {
		t.Fatal(err)
	}
	if settings.DatabaseURL != got {
		t.Fatal("备份未使用统一连接配置")
	}
}

func TestDatabasePasswordFileFailures(t *testing.T) {
	for _, tt := range []struct {
		name, address, content string
		missing                bool
	}{
		{"缺失文件", "postgres://roomusic@postgres/roomusic", "", true},
		{"空文件", "postgres://roomusic@postgres/roomusic", "", false},
		{"多行", "postgres://roomusic@postgres/roomusic", "secret\nsecond", false},
		{"零字节", "postgres://roomusic@postgres/roomusic", "secret\x00", false},
		{"重复密码", "postgres://roomusic:secret@postgres/roomusic", "other", false},
		{"查询密码", "postgres://roomusic@postgres/roomusic?password=secret", "other", false},
		{"地址无用户名", "postgres://postgres/roomusic", "secret", false},
		{"无效地址", "postgres://roomusic:secret%zz@postgres/roomusic", "other", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "password")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("ROOMUSIC_DATABASE_URL", tt.address)
			t.Setenv("ROOMUSIC_DB_PASSWORD_FILE", path)
			_, err := DatabaseURL()
			if err == nil {
				t.Fatal("应拒绝无效配置")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), path) {
				t.Fatal("错误暴露凭据或路径")
			}
		})
	}
}

func TestDatabaseURLWithoutPasswordFile(t *testing.T) {
	t.Setenv("ROOMUSIC_DB_PASSWORD_FILE", "")
	want := "postgres://roomusic:existing@localhost/roomusic"
	t.Setenv("ROOMUSIC_DATABASE_URL", want)
	got, err := DatabaseURL()
	if err != nil || got != want {
		t.Fatalf("现有配置行为变化：%v", err)
	}
}
