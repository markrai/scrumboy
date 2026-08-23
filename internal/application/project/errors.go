package project

import "errors"

// ErrActorRequired reports that a lifecycle operation requiring an
// authenticated actor did not find one in its trusted context.
var ErrActorRequired = errors.New("project lifecycle actor required")
