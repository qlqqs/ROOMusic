package tool

import (
	"context"
	"log/slog"
)

// LogConfigChanged 只记录后端身份和稳定目标标识，不记录配置值或工具正文。
func LogConfigChanged(ctx context.Context, logger *slog.Logger, caller Caller, name, targetID string) {
	if logger == nil {
		return
	}
	logger.InfoContext(ctx, "管家配置已更新", "event", "steward.config.changed", "message", "管家配置已更新", "module", "steward",
		"tool", name, "actor_id", caller.User.ID, "source", caller.Source, "target_id", targetID,
		"conversation_id", caller.ConversationID, "token_id", caller.TokenID, "request_id", caller.RequestID)
}
