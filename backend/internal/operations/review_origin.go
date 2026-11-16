package operations

import "context"

type reviewOriginKey struct{}
type reviewOrigin struct{ ID, Decision string }

// WithReviewOrigin 由后端装配层补充审查来源，不改变指纹或执行权限。
func WithReviewOrigin(ctx context.Context, id, decision string) context.Context {
	return context.WithValue(ctx, reviewOriginKey{}, reviewOrigin{ID: id, Decision: decision})
}
