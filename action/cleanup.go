package action

import (
	"context"

	"github.com/aws/aws-sdk-go/aws/session"
)

const (
	DeletionTag = "aws-janitor/marked-for-deletion"
)

type CleanupScope struct {
	Session   *session.Session
	Commit    bool
	IgnoreTag string
}

type CleanupFunc func(ctx context.Context, input *CleanupScope) error
