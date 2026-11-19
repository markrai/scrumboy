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

type GitHubItem struct {
    ResourceID int64 `json:"resourceId,omitempty"`
    Repository string `json:"repository,omitempty"`
    Availability string `json:"availability,omitempty"`
    Kind string `json:"kind"`
    Number int64 `json:"number"`
    Title string `json:"title"`
    State string `json:"state"`
    Draft bool `json:"draft"`
    Merged bool `json:"merged"`
    URL string `json:"url"`
}

type IssuePullRequestProvider interface {
    ResolveItem(context.Context,string,string,string,string,int64) (GitHubItem,error)
    SearchItems(context.Context,string,string,string,string,string) ([]GitHubItem,error)
}

type Provider interface {
	Installations(context.Context) ([]Installation, error)
	Resources(context.Context, string) ([]Resource, error)
	Invalidate(string)
	Revoke(context.Context,string) error
}
