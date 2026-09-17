package audit

import "context"

type Repository interface {
	Append(context.Context, Entry) error
	List(context.Context, Filter) ([]Entry, error)
}
