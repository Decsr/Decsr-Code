package agent

import (
	"context"

	"github.com/Decsr/Decsr-Code/internal/bus"
)

// Agent is implemented by every specialized agent in the pipeline.
type Agent interface {
	Role() bus.AgentRole
	Run(ctx context.Context, msg bus.Message) (bus.Message, error)
}
