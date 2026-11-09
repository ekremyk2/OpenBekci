package obs

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/otel/attribute"
)

// JobMiddleware times and traces every worked River job.
type JobMiddleware struct{ river.WorkerMiddlewareDefaults }

// Work implements rivertype.WorkerMiddleware.
func (*JobMiddleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	ctx, sp := Start(ctx, "job "+job.Kind, attribute.String("job.kind", job.Kind), attribute.Int("job.attempt", job.Attempt))
	start := time.Now()
	err := doInner(ctx)
	Job(job.Kind, time.Since(start), err)
	End(sp, err)
	return err
}
