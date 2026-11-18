package integration

import "context"

type Installation struct {
	ID string `json:"id"`
	Account string `json:"account"`
	Kind string `json:"kind"`
}

type Resource struct {
	ID string `json:"id"`
	Name string `json:"name"`
	URL string `json:"url"`
}

type Provider interface {
	Installations(context.Context) ([]Installation, error)
	Resources(context.Context, string) ([]Resource, error)
	Invalidate(string)
	Revoke(context.Context,string) error
}
