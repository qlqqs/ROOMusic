package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// EncryptedKey 是调用方加密后的密钥，存储层不接收明文或执行加解密。
type EncryptedKey struct {
	// Ciphertext 包含调用方生成的完整密文。
	Ciphertext []byte `json:"-"`
	// LastFour 是密钥末四位；短密钥可以少于四个字符。
	LastFour string
}

// ProbeResult 保存最近一次探测结果及其时间。
type ProbeResult struct {
	// Result 是适配器提供的探测结果 JSON。
	Result json.RawMessage
	// ProbedAt 是实际探测时间。
	ProbedAt time.Time
}

// UserSettings 是当前用户的模型设置；传输层应单独投影响应，不返回密文。
type UserSettings struct {
	SearchSource string
	SearXNGURL   string
	SearchKey    *EncryptedKey `json:"-"`
	// UserID 是设置所属用户。
	UserID string
	// Endpoint 是模型端点地址，空字符串表示未配置。
	Endpoint string
	// Key 是已有密钥；尚未填写时为 nil。
	Key *EncryptedKey `json:"-"`
	// Protocol 是 openai、messages 或 responses，空字符串表示未配置。
	Protocol string
	// Model 是对话模型，空字符串表示未配置。
	ReviewModel string
	Model       string
	// DisplayName 为空时使用默认显示名字。
	DisplayName string
	// Probe 是最近一次探测；尚未探测时为 nil。
	Probe *ProbeResult
	// Overrides 保存用户手动覆盖项。
	Overrides json.RawMessage
	// UpdatedAt 是最近一次保存时间。
	UpdatedAt time.Time
}

// IsConfigured 判断端点、协议与模型是否均已填写，密钥可选。
func (settings UserSettings) IsConfigured() bool {
	return settings.Endpoint != "" && settings.Protocol != "" && settings.Model != ""
}

// UserSettingsInput 是保存设置的输入，普通字段整体替换，可选字段按注释保留旧值。
// 首次只保存显示名字时，端点、协议与模型使用零值，Key 为 nil。
type UserSettingsInput struct {
	SearchSource   string
	SearXNGURL     string
	SearchKey      *EncryptedKey
	ClearSearchKey bool
	// ClearKey 显式删除已保存的密钥。
	ClearKey bool
	// ClearProbe 清除旧探测结果与时间。
	ClearProbe bool
	// Endpoint 是模型端点地址，空字符串表示未配置。
	Endpoint string
	// Key 为 nil 时保留已有密文与末四位。
	Key *EncryptedKey
	// Protocol 是 openai、messages 或 responses，空字符串表示未配置。
	Protocol string
	// Model 是对话模型，空字符串表示未配置。
	ReviewModel string
	Model       string
	// DisplayName 为空时恢复默认显示名字。
	DisplayName string
	// Probe 为 nil 时保留上次探测结果与时间。
	Probe *ProbeResult
	// Overrides 为 nil 时保留已有覆盖项，传入 {} 可清空覆盖项。
	Overrides json.RawMessage
}

const settingsColumns = `user_id::text,endpoint,key_ciphertext,key_last_four,protocol,model,
 display_name,probe_result,probed_at,overrides,updated_at,review_model,search_source,searxng_url,search_key_ciphertext,search_key_last_four`

// GetUserSettings 读取当前用户的设置，尚未保存时返回零值设置与 nil 错误。
func (s *Store) GetUserSettings(ctx context.Context, userID string) (UserSettings, error) {
	return ReadUserSettings(ctx, s.db, userID)
}

// ReadUserSettings 支持在读取整理开关的同一事务内核对开启者的管家设置。
func ReadUserSettings(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID string) (UserSettings, error) {
	settings, err := scanSettings(q.QueryRowContext(ctx, `SELECT `+settingsColumns+`
 FROM steward_user_settings WHERE user_id=$1::uuid`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return UserSettings{SearchSource: "none"}, nil
	}
	return settings, storeError("读取管家设置", err)
}

// SaveUserSettings 保存当前用户的设置，未提供新密钥时原子地保留原密文。
// 允许尚未配置模型端点时只保存显示名字，空端点、协议和模型按原值写入。
func (s *Store) SaveUserSettings(ctx context.Context, userID string, input UserSettingsInput) (UserSettings, error) {
	return s.UpdateUserSettings(ctx, userID, func(UserSettings) (UserSettingsInput, error) { return input, nil })
}

// UpdateUserSettings 在同一事务中锁定、读取并修改设置，回调失败不保存任何字段。
func (s *Store) UpdateUserSettings(ctx context.Context, userID string, update func(UserSettings) (UserSettingsInput, error)) (UserSettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserSettings{}, storeError("开始修改管家设置", err)
	}
	defer tx.Rollback()
	// 先建立默认行，使首次保存也参与同一用户的行锁串行化；失败随事务回滚。
	if _, err := tx.ExecContext(ctx, `INSERT INTO steward_user_settings (user_id) VALUES ($1::uuid) ON CONFLICT DO NOTHING`, userID); err != nil {
		return UserSettings{}, storeError("准备管家设置", err)
	}
	current, err := scanSettings(tx.QueryRowContext(ctx, `SELECT `+settingsColumns+` FROM steward_user_settings WHERE user_id=$1::uuid FOR UPDATE`, userID))
	if err != nil {
		return UserSettings{}, storeError("锁定管家设置", err)
	}
	input, err := update(current)
	if err != nil {
		return UserSettings{}, err
	}
	saved, err := saveUserSettings(ctx, tx, userID, input)
	if err != nil {
		return UserSettings{}, err
	}
	return saved, storeError("提交管家设置", tx.Commit())
}

func saveUserSettings(ctx context.Context, tx *sql.Tx, userID string, input UserSettingsInput) (UserSettings, error) {
	var ciphertext []byte
	var lastFour string
	if input.Key != nil {
		ciphertext, lastFour = input.Key.Ciphertext, input.Key.LastFour
	}
	var searchCiphertext []byte
	var searchLastFour string
	if input.SearchKey != nil {
		searchCiphertext, searchLastFour = input.SearchKey.Ciphertext, input.SearchKey.LastFour
	}
	if input.SearchSource == "" {
		input.SearchSource = "none"
	}
	var probe json.RawMessage
	var probedAt *time.Time
	if input.Probe != nil {
		probe, probedAt = input.Probe.Result, &input.Probe.ProbedAt
	}
	settings, err := scanSettings(tx.QueryRowContext(ctx, `INSERT INTO steward_user_settings
 (user_id,endpoint,key_ciphertext,key_last_four,protocol,model,display_name,probe_result,probed_at,overrides,review_model,search_source,searxng_url,search_key_ciphertext,search_key_last_four)
 VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,COALESCE($10::jsonb,'{}'::jsonb),$13,$14,$15,$16,$17)
 ON CONFLICT (user_id) DO UPDATE SET endpoint=EXCLUDED.endpoint,
 key_ciphertext=CASE WHEN $11 THEN EXCLUDED.key_ciphertext ELSE steward_user_settings.key_ciphertext END,
 key_last_four=CASE WHEN $11 THEN EXCLUDED.key_last_four ELSE steward_user_settings.key_last_four END,
 search_source=EXCLUDED.search_source,searxng_url=EXCLUDED.searxng_url,
 search_key_ciphertext=CASE WHEN $18 THEN EXCLUDED.search_key_ciphertext ELSE steward_user_settings.search_key_ciphertext END,
 search_key_last_four=CASE WHEN $18 THEN EXCLUDED.search_key_last_four ELSE steward_user_settings.search_key_last_four END,
 review_model=EXCLUDED.review_model,protocol=EXCLUDED.protocol,model=EXCLUDED.model,display_name=EXCLUDED.display_name,
 probe_result=CASE WHEN $12 THEN EXCLUDED.probe_result ELSE steward_user_settings.probe_result END,
 probed_at=CASE WHEN $12 THEN EXCLUDED.probed_at ELSE steward_user_settings.probed_at END,
 overrides=COALESCE($10::jsonb,steward_user_settings.overrides),updated_at=clock_timestamp()
 RETURNING `+settingsColumns, userID, input.Endpoint, ciphertext, lastFour, input.Protocol,
		input.Model, input.DisplayName, probe, probedAt, input.Overrides, input.Key != nil || input.ClearKey, input.Probe != nil || input.ClearProbe, input.ReviewModel, input.SearchSource, input.SearXNGURL, searchCiphertext, searchLastFour, input.SearchKey != nil || input.ClearSearchKey))
	return settings, storeError("保存管家设置", err)
}

func scanSettings(row scanner) (UserSettings, error) {
	var settings UserSettings
	var key, searchKey EncryptedKey
	var probe json.RawMessage
	var probedAt *time.Time
	err := row.Scan(&settings.UserID, &settings.Endpoint, &key.Ciphertext, &key.LastFour,
		&settings.Protocol, &settings.Model, &settings.DisplayName, (*[]byte)(&probe), &probedAt,
		(*[]byte)(&settings.Overrides), &settings.UpdatedAt, &settings.ReviewModel, &settings.SearchSource, &settings.SearXNGURL, &searchKey.Ciphertext, &searchKey.LastFour)
	if err != nil {
		return UserSettings{}, err
	}
	if searchKey.Ciphertext != nil {
		settings.SearchKey = &searchKey
	}
	if key.Ciphertext != nil {
		settings.Key = &key
	}
	if probedAt != nil {
		settings.Probe = &ProbeResult{Result: probe, ProbedAt: probedAt.UTC()}
	}
	settings.UpdatedAt = settings.UpdatedAt.UTC()
	return settings, nil
}

// GetMembersEnabled 读取实例成员开关，初始值由迁移设为允许。
func (s *Store) GetMembersEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT members_enabled FROM steward_instance_settings WHERE id=1`).Scan(&enabled)
	return enabled, storeError("读取管家成员开关", err)
}

// SetMembersEnabled 保存实例成员开关，调用方负责管理员鉴权。
func (s *Store) SetMembersEnabled(ctx context.Context, enabled bool) error {
	err := requireAffected(s.db.ExecContext(ctx, `UPDATE steward_instance_settings SET members_enabled=$1 WHERE id=1`, enabled))
	return storeError("保存管家成员开关", err)
}

// HasConfiguredModel 与 IsConfigured 使用相同条件，仅返回是否已有可用配置。
func (s *Store) HasConfiguredModel(ctx context.Context) (bool, error) {
	var configured bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM steward_user_settings
        WHERE endpoint <> '' AND protocol <> '' AND model <> '')`).Scan(&configured)
	return configured, err
}
