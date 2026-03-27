package requestid

import "context"

// ctxKey — неэкспортируемый тип ключа для context.Value.
type ctxKey struct{}

// With кладёт идентификатор запроса в контекст (для job-логов доставки).
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From возвращает request_id из контекста или пустую строку.
func From(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}
