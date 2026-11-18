package integration

import "context"

type Installation struct {
	ID int64 `json:"id"`
	Account string `json:"account"`
	Kind string `json:"kind"`
}

type Resource struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
	URL string `json:"url"`
}

type Provider interface {
	Installations(context.Context) ([]Installation, error)
	Resources(context.Context, int64) ([]Resource, error)
	Invalidate(int64)
}
