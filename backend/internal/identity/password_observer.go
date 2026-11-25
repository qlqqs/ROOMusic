package identity

import (
	"context"
	"database/sql"
)

// PasswordObserver 在已锁定账号的事务中接收已核对或即将生效的密码。
type PasswordObserver interface {
	ObservePassword(ctx context.Context, tx *sql.Tx, userID, password string) error
}

func observePassword(ctx context.Context, tx *sql.Tx, userID, password string, observers []PasswordObserver) error {
	for _, observer := range observers {
		if observer != nil {
			if err := observer.ObservePassword(ctx, tx, userID, password); err != nil {
				return err
			}
		}
	}
	return nil
}
